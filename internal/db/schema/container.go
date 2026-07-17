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
		// docker_id is the on-the-wire Docker engine container ID
		// returned by POST /containers/create. It is *optional* and
		// *nillable* because the compose deploy path produces a
		// stack of containers (one per service) and the current
		// schema records only one Container row per deploy — there
		// is no single "primary" container to key on. The docker
		// deploy path captures the ID and sets it; the compose
		// path leaves it nil.
		field.String("docker_id").
			Optional().
			Nillable().
			Comment("Docker container ID. Set for docker-mode deploys; nil for compose-mode (no single primary container)."),
		// name is the on-the-wire container name, e.g. nanoku-myapp-<hex>.
		// It is informational — the network alias (nanoku-<app> for
		// docker, nanoku-<app>-<service> for compose) is the routing
		// identity and does not rotate when the container does. UNIQUE
		// removed: nothing else keys on the literal name anymore, so a
		// stale row from a failed deploy no longer blocks the next one.
		field.String("name").
			Comment("Container name (e.g. nanoku-myapp-<hex>). Informational only; routing uses the network alias."),
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