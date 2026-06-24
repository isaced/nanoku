package api

import (
	"context"
	"fmt"

	"github.com/isaced/nanoku/internal/secret"
)

// MigrateEncryption walks every sensitive column (app registry_password /
// trigger_token, envvar value) and re-encrypts any row that still holds
// plaintext. Idempotent — safe to run on every boot; rows that already carry
// the "enc:" prefix are skipped. Returns the first error encountered and
// stops, so a partial migration never leaves the DB half-converted.
//
// Pre-release data uses plaintext rows from early builds; production
// installs that bootstrapped from an encrypted-only schema will see this as
// a fast no-op.
func (h *Handlers) MigrateEncryption(ctx context.Context) error {
	if h.DB == nil || h.Secret == nil {
		return fmt.Errorf("MigrateEncryption: db or secret not initialized")
	}

	apps, err := h.DB.App.Query().All(ctx)
	if err != nil {
		return fmt.Errorf("load apps: %w", err)
	}
	for _, a := range apps {
		changed := false
		upd := h.DB.App.UpdateOneID(a.ID)
		if a.TriggerToken != nil && *a.TriggerToken != "" && !secret.IsEncrypted(*a.TriggerToken) {
			enc, err := h.Secret.EncryptString(*a.TriggerToken)
			if err != nil {
				return fmt.Errorf("encrypt trigger_token app %d: %w", a.ID, err)
			}
			upd.SetTriggerToken(enc)
			changed = true
		}
		if a.RegistryPassword != nil && *a.RegistryPassword != "" && !secret.IsEncrypted(*a.RegistryPassword) {
			enc, err := h.Secret.EncryptString(*a.RegistryPassword)
			if err != nil {
				return fmt.Errorf("encrypt registry_password app %d: %w", a.ID, err)
			}
			upd.SetRegistryPassword(enc)
			changed = true
		}
		if changed {
			if _, err := upd.Save(ctx); err != nil {
				return fmt.Errorf("save app %d: %w", a.ID, err)
			}
		}
	}

	envs, err := h.DB.EnvVar.Query().All(ctx)
	if err != nil {
		return fmt.Errorf("load envvars: %w", err)
	}
	for _, e := range envs {
		if e.Value == "" || secret.IsEncrypted(e.Value) {
			continue
		}
		enc, err := h.Secret.EncryptString(e.Value)
		if err != nil {
			return fmt.Errorf("encrypt envvar %d: %w", e.ID, err)
		}
		if _, err := h.DB.EnvVar.UpdateOneID(e.ID).SetValue(enc).Save(ctx); err != nil {
			return fmt.Errorf("save envvar %d: %w", e.ID, err)
		}
	}
	return nil
}