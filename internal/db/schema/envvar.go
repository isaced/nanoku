package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type EnvVar struct{ ent.Schema }

func (EnvVar) Mixin() []ent.Mixin {
	return []ent.Mixin{TimeMixin{}}
}

func (EnvVar) Fields() []ent.Field {
	return []ent.Field{
		field.String("key").
			MaxLen(255).
			Comment("Env var name, e.g. DATABASE_URL."),
		field.String("value").
			Sensitive().
			Comment("Env var value. Plain text in v1; encryption in v2."),
	}
}

func (EnvVar) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("app", App.Type).
			Ref("env_vars").
			Unique(),
	}
}

func (EnvVar) Indexes() []ent.Index {
	return []ent.Index{
		index.Edges("app").Fields("key").Unique(),
	}
}