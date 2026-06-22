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
			Comment("Docker image, e.g. nginx:1.27."),
		field.Int("port").
			Min(1).Max(65535).
			Comment("Internal port the app listens on."),
		field.String("repo_url").
			Optional().
			Nillable().
			Comment("Git repo URL for future webhook-driven deploys."),
		field.String("branch").
			Default("main"),
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