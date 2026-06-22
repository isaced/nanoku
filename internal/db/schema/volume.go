package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type Volume struct{ ent.Schema }

func (Volume) Mixin() []ent.Mixin {
	return []ent.Mixin{TimeMixin{}}
}

func (Volume) Fields() []ent.Field {
	return []ent.Field{
		field.Enum("type").
			Values("volume", "bind").
			Comment("volume = named docker volume; bind = host path bind mount."),
		field.String("source").
			Optional().
			Nillable().
			Comment("Volume name (type=volume) or host path (type=bind). Empty for type=volume means nanoku auto-names it."),
		field.String("target").
			Comment("Container path to mount at, must be absolute."),
		field.Bool("read_only").
			Default(false),
	}
}

func (Volume) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("app", App.Type).
			Ref("volumes").
			Unique(),
	}
}

func (Volume) Indexes() []ent.Index {
	return []ent.Index{
		index.Edges("app").Fields("target").Unique(),
	}
}
