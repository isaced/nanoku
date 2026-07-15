package api

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// TestImportExposedPortsFromCompose is the unit test for the YAML
// parser that powers the "Import from compose" button. The cases
// below are the realistic docker-compose v2/v3 shapes we expect
// to see in the wild, not abstract corner cases — the goal is to
// catch breakage the first time someone pastes a `ports: [80]`
// list instead of `ports: - 80`.
func TestImportExposedPortsFromCompose(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		want    []ExposedPort
		wantErr bool
	}{
		{
			name: "empty content is a valid no-op",
			yaml: "",
			want: nil,
		},
		{
			name: "whitespace only",
			yaml: "   \n\n",
			want: nil,
		},
		{
			name: "single service with expose",
			yaml: `
services:
  web:
    image: nginx:1.27
    expose:
      - 3000
`,
			want: []ExposedPort{{Name: "web", Port: 3000}},
		},
		{
			name: "single service with host:container port takes container half",
			yaml: `
services:
  web:
    image: nginx:1.27
    ports:
      - "8080:80"
`,
			want: []ExposedPort{{Name: "web", Port: 80}},
		},
		{
			name: "bare integer port",
			yaml: `
services:
  web:
    image: nginx:1.27
    ports:
      - 80
`,
			want: []ExposedPort{{Name: "web", Port: 80}},
		},
		{
			name: "long-form port with bind IP",
			yaml: `
services:
  web:
    image: nginx:1.27
    ports:
      - "127.0.0.1:8080:80"
`,
			want: []ExposedPort{{Name: "web", Port: 80}},
		},
		{
			name: "bare string port",
			yaml: `
services:
  web:
    image: nginx:1.27
    ports:
      - "80"
`,
			want: []ExposedPort{{Name: "web", Port: 80}},
		},
		{
			name: "expose wins over ports when both are set",
			yaml: `
services:
  web:
    image: nginx:1.27
    expose:
      - 3000
    ports:
      - "8080:80"
`,
			want: []ExposedPort{{Name: "web", Port: 3000}},
		},
		{
			name: "no ports and no expose → port=0 (UI surfaces empty input)",
			yaml: `
services:
  web:
    image: nginx:1.27
`,
			want: []ExposedPort{{Name: "web", Port: 0}},
		},
		{
			name: "multi-service mixed ports",
			yaml: `
services:
  web:
    image: nginx:1.27
    ports: ["8080:80"]
  api:
    image: myapi:1
    expose: [8080]
  db:
    image: postgres:16
    ports:
      - "5432"
`,
			want: []ExposedPort{
				{Name: "api", Port: 8080},
				{Name: "db", Port: 5432},
				{Name: "web", Port: 80},
			},
		},
		{
			name: "empty services block is a valid no-op",
			yaml: `services: {}`,
			want: nil,
		},
		{
			name: "no services key at all",
			yaml: `version: "3"
volumes:
  data: {}
`,
			want: nil,
		},
		{
			name: "result is sorted by service name (stable order)",
			yaml: `
services:
  zebra: {image: z:1}
  apple: {image: a:1}
  mango: {image: m:1}
`,
			want: []ExposedPort{
				{Name: "apple", Port: 0},
				{Name: "mango", Port: 0},
				{Name: "zebra", Port: 0},
			},
		},
		{
			name:    "invalid YAML is an error",
			yaml:    "services: : :",
			wantErr: true,
		},
		{
			name: "non-integer expose entry is skipped, no port found",
			yaml: `
services:
  web:
    expose:
      - "abc"
`,
			want: []ExposedPort{{Name: "web", Port: 0}},
		},
		{
			name: "first port entry used when list has many",
			yaml: `
services:
  web:
    ports:
      - "8080:80"
      - "8443:443"
`,
			want: []ExposedPort{{Name: "web", Port: 80}},
		},
		{
			name: "port out of range falls through to 0",
			yaml: `
services:
  web:
    ports:
      - "99999"
`,
			want: []ExposedPort{{Name: "web", Port: 0}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ImportExposedPortsFromCompose(tc.yaml)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (got=%+v)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestImportExposedPortsHandler covers the HTTP wiring: docker
// apps refuse with 400, missing content refuses with 400, a
// compose app with inline content returns the parsed list.
func TestImportExposedPortsHandler(t *testing.T) {
	t.Run("docker app refuses with 400", func(t *testing.T) {
		h := newSiteTestHandlers(t)
		a := seedAppDocker(t, h, "blog")
		w := importExposedPorts(t, h, a.ID)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", w.Code)
		}
	})

	t.Run("compose app with content returns parsed list", func(t *testing.T) {
		h := newSiteTestHandlers(t)
		a := seedAppCompose(t, h, "kuma", nil)
		// Override compose_content with a richer YAML.
		_, err := h.DB.App.UpdateOneID(a.ID).
			SetComposeContent(`
services:
  uptime-kuma:
    image: louislam/uptime-kuma:2
    ports: ["3001:3001"]
  sidecar:
    image: busybox:1
    expose: [9090]
`).
			Save(context.Background())
		if err != nil {
			t.Fatalf("update compose_content: %v", err)
		}

		w := importExposedPorts(t, h, a.ID)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
		}
		var resp struct {
			ExposedPorts []ExposedPort `json:"exposedPorts"`
			Hint         string        `json:"hint"`
		}
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		want := []ExposedPort{
			{Name: "sidecar", Port: 9090},
			{Name: "uptime-kuma", Port: 3001},
		}
		if !reflect.DeepEqual(resp.ExposedPorts, want) {
			t.Errorf("exposedPorts = %+v, want %+v", resp.ExposedPorts, want)
		}
	})

	t.Run("compose app with no content refuses with 400", func(t *testing.T) {
		h := newSiteTestHandlers(t)
		// Direct DB create with no compose_content so the inline
		// string is empty.
		a, err := h.DB.App.Create().
			SetName("nocontent").
			SetDeployMethod("compose").
			Save(context.Background())
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
		w := importExposedPorts(t, h, a.ID)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body = %s", w.Code, w.Body.String())
		}
	})

	t.Run("compose_path is set refuses with actionable error", func(t *testing.T) {
		h := newSiteTestHandlers(t)
		created, err := h.DB.App.Create().
			SetName("withpath").
			SetDeployMethod("compose").
			SetComposePath("/tmp/whatever.yml").
			Save(context.Background())
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
		w := importExposedPorts(t, h, created.ID)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", w.Code)
		}
		if !strings.Contains(w.Body.String(), "compose_path") {
			t.Errorf("body should mention compose_path: %s", w.Body.String())
		}
	})

	t.Run("missing app is 404", func(t *testing.T) {
		h := newSiteTestHandlers(t)
		w := importExposedPorts(t, h, 9999)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", w.Code)
		}
	})
}
