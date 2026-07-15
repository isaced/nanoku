package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type Container struct{ ent.Schema }

func (Container) Mixin() []ent.Mixin {
	return []ent.Mixin{TimeMixin{}}
}

func (Container) Fields() []ent.Field {
	return []ent.Field{
		field.String("docker_id").
			Comment("Docker container ID."),
		field.String("name").
			Unique().
			Comment("Container name, e.g. nanoku-myapp."),
		field.String("image").
			Comment("Resolved image reference."),
		field.Enum("status").
			Values("created", "running", "paused", "restarting", "removing", "exited", "dead", "retired"),

		field.Time("started_at").
			Optional().Nillable(),
		field.Time("stopped_at").
			Optional().Nillable(),
	}
}

func (Container) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("app", App.Type).
			Ref("containers").
			Unique(),
		edge.From("deploy", Deploy.Type).
			Ref("container").
			Unique().
			Comment("The deploy that produced this container."),
		edge.From("current_for", App.Type).
			Ref("current_container").
			Unique().
			Comment("Inverse of App.current_container."),
	}
}

func (Container) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("status"),
	}
}