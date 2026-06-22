package schema

import (
	"regexp"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

type App struct{ ent.Schema }

func (App) Mixin() []ent.Mixin {
	return []ent.Mixin{TimeMixin{}}
}

func (App) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").
			Unique().
			Match(regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)).
			Comment("DNS-1123 label; used as Docker container prefix."),
		field.String("image").
			Optional().
			Nillable().
			Comment("Docker image, e.g. nginx:1.27. Ignored when deploy_method=compose."),
		field.Int("port").
			Min(1).Max(65535).
			Optional().
			Comment("Internal port the app listens on. Ignored when deploy_method=compose."),
		field.String("repo_url").
			Optional().
			Nillable().
			Comment("Git repo URL for future webhook-driven deploys."),
		field.String("branch").
			Default("main"),

		// "docker" = run a single container from `image` (current behavior).
		// "compose" = run `docker compose up -d` against `compose_content` (or
		// `compose_path` if set, in which case content is ignored).
		field.String("deploy_method").
			Default("docker").
			Comment("docker | compose"),
		field.Text("compose_content").
			Optional().
			Nillable().
			Comment("Inline compose YAML used when deploy_method=compose."),
		field.String("compose_path").
			Optional().
			Nillable().
			Comment("Path to an existing compose file on the host. If set, overrides compose_content."),

		// Private registry credentials. Logged in to before pull / compose pull
		// and logged out after, so credentials are not left in
		// ~/.docker/config.json permanently.
		// TODO: encrypt at rest before any real deployment.
		field.String("registry_url").
			Optional().
			Nillable().
			Comment("Registry hostname, e.g. ghcr.io or registry.example.com. Empty = use the public registry configured on the host."),
		field.String("registry_username").
			Optional().
			Nillable(),
		field.String("registry_password").
			Optional().
			Nillable().
			Sensitive(),
	}
}

func (App) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("sites", Site.Type),
		edge.To("containers", Container.Type),
		edge.To("deploys", Deploy.Type),
		edge.To("env_vars", EnvVar.Type),
		edge.To("current_container", Container.Type).
			Unique().
			Comment("The container currently serving traffic for this app."),
	}
}
