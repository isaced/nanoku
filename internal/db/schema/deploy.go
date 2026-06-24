package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type Deploy struct{ ent.Schema }

func (Deploy) Mixin() []ent.Mixin {
	return []ent.Mixin{TimeMixin{}}
}

func (Deploy) Fields() []ent.Field {
	return []ent.Field{
		field.String("commit_sha").
			Optional().Nillable().
			Comment("Git commit SHA. Optional for manual deploys."),
		field.String("commit_message").
			Optional().Nillable(),
		field.Enum("trigger").
			Values("manual", "trigger", "rollback").
			Default("manual"),
		field.Enum("status").
			Values("pending", "running", "success", "failed", "rolled_back").
			Default("pending"),
		field.String("error").
			Optional().Nillable().
			Comment("Failure reason when status=failed."),
		field.Time("started_at").
			Optional().Nillable(),
		field.Time("finished_at").
			Optional().Nillable(),
		field.String("image").
			Optional().Nillable().
			Comment("Image reference actually deployed (e.g. nginx:1.27 or ghcr.io/me/app@sha256:...). Snapshot kept so rollback can re-pull the same image."),
	}
}

func (Deploy) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("app", App.Type).
			Ref("deploys").
			Unique(),
		edge.To("container", Container.Type).
			Unique().
			Comment("Container produced by this deploy."),
	}
}

func (Deploy) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("status"),
		index.Fields("created_at"),
	}
}