package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/isaced/nanoku/internal/db"
	"github.com/isaced/nanoku/internal/db/app"
	"github.com/isaced/nanoku/internal/secret"
)

func seedAppWithPlaintextCreds(t *testing.T, d *db.DB, name, token, regPass string, envValue string) {
	t.Helper()
	a, err := d.App.Create().
		SetName(name).
		SetImage("nginx:1.27").
		SetPort(80).
		SetRegistryURL("ghcr.io").
		SetRegistryUsername("me").
		SetRegistryPassword(regPass).
		SetTriggerToken(token).
		Save(context.Background())
	if err != nil {
		t.Fatalf("seed app: %v", err)
	}
	if envValue != "" {
		if _, err := d.EnvVar.Create().
			SetAppID(a.ID).
			SetKey("SECRET").
			SetValue(envValue).
			Save(context.Background()); err != nil {
			t.Fatalf("seed envvar: %v", err)
		}
	}
}

func TestMigrateEncryption_ReEncryptsPlaintextRows(t *testing.T) {
	d := newTestDB(t)
	seedAppWithPlaintextCreds(t, d, "blog", "plaintext-token", "plaintext-pass", "plaintext-env")

	h := &Handlers{DB: d, Secret: newTestSealer(t)}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.MigrateEncryption(ctx); err != nil {
		t.Fatalf("MigrateEncryption: %v", err)
	}

	a, err := d.App.Query().Where(app.Name("blog")).Only(ctx)
	if err != nil {
		t.Fatalf("reload app: %v", err)
	}
	if !secret.IsEncrypted(*a.TriggerToken) {
		t.Errorf("trigger_token still plaintext: %q", *a.TriggerToken)
	}
	if !secret.IsEncrypted(*a.RegistryPassword) {
		t.Errorf("registry_password still plaintext: %q", *a.RegistryPassword)
	}
	envs, _ := d.EnvVar.Query().All(ctx)
	if len(envs) != 1 || !secret.IsEncrypted(envs[0].Value) {
		t.Errorf("envvar value not encrypted: %+v", envs)
	}

	// Decryption must round-trip to the original values.
	tok, err := h.Secret.DecryptString(*a.TriggerToken)
	if err != nil || tok != "plaintext-token" {
		t.Errorf("trigger_token round-trip: got %q err=%v", tok, err)
	}
	reg, err := h.Secret.DecryptString(*a.RegistryPassword)
	if err != nil || reg != "plaintext-pass" {
		t.Errorf("registry_password round-trip: got %q err=%v", reg, err)
	}
	env, err := h.Secret.DecryptString(envs[0].Value)
	if err != nil || env != "plaintext-env" {
		t.Errorf("envvar round-trip: got %q err=%v", env, err)
	}
}

func TestMigrateEncryption_Idempotent(t *testing.T) {
	d := newTestDB(t)
	seedAppWithPlaintextCreds(t, d, "blog", "tok", "pw", "ev")

	h := &Handlers{DB: d, Secret: newTestSealer(t)}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i := 0; i < 3; i++ {
		if err := h.MigrateEncryption(ctx); err != nil {
			t.Fatalf("MigrateEncryption round %d: %v", i, err)
		}
	}
	// After multiple runs, values must still decrypt to the originals and
	// must still carry the enc: prefix. A re-encryption loop would produce
	// different ciphertext each time, which would fail the round-trip.
	a, _ := d.App.Query().Where(app.Name("blog")).Only(ctx)
	tok, err := h.Secret.DecryptString(*a.TriggerToken)
	if err != nil || tok != "tok" {
		t.Errorf("post-idempotent token: got %q err=%v", tok, err)
	}
}

func TestMigrateEncryption_SkipsEmptyValues(t *testing.T) {
	d := newTestDB(t)
	// App with no trigger_token / registry_password set.
	if _, err := d.App.Create().
		SetName("blank").
		SetImage("nginx:1.27").
		SetPort(80).
		Save(context.Background()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	h := &Handlers{DB: d, Secret: newTestSealer(t)}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.MigrateEncryption(ctx); err != nil {
		t.Fatalf("MigrateEncryption: %v", err)
	}
	a, _ := d.App.Query().Where(app.Name("blank")).Only(ctx)
	if a.TriggerToken != nil {
		t.Errorf("trigger_token should remain nil, got %q", *a.TriggerToken)
	}
	if a.RegistryPassword != nil {
		t.Errorf("registry_password should remain nil, got %q", *a.RegistryPassword)
	}
}

func TestTriggerEndpoint_VerifiesEncryptedToken(t *testing.T) {
	// Round-trip: a token produced by the create path (encrypted at rest)
	// must verify correctly on the trigger endpoint.
	h, a := newTestHandlers(t, false)
	tok, err := h.Secret.EncryptString("round-trip-token-xyz")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if _, err := h.DB.App.UpdateOneID(a.ID).SetTriggerToken(tok).Save(context.Background()); err != nil {
		t.Fatalf("seed encrypted token: %v", err)
	}

	body := []byte(`{"tag":"v1.0.0"}`)
	w := postTrigger(t, h, testAppName, body, map[string]string{
		authHeader: bearer(t, "round-trip-token-xyz"),
	})
	// 202 because Docker=nil makes the actual deploy fall through, but the
	// auth path must have accepted our token. Any 401 here means
	// decryption silently failed.
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"accepted":true`) {
		t.Errorf("expected accepted:true, body=%s", w.Body.String())
	}
}