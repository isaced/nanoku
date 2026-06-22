package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type Site struct{ ent.Schema }

func (Site) Mixin() []ent.Mixin {
	return []ent.Mixin{TimeMixin{}}
}

func (Site) Fields() []ent.Field {
	return []ent.Field{
		field.String("domain").
			Unique().
			Comment("Public domain (e.g. api.example.com). Wildcards not supported."),
		field.String("upstream").
			Comment("Upstream URL or host:port for reverse_proxy."),
		field.Bool("enabled").
			Default(true).
			Comment("When false, site is omitted from generated Caddyfile."),
		field.Enum("scheme").
			Values("http", "https").
			Default("https").
			Comment("Listener scheme. http forces Caddy to bind :80 and skip auto-HTTPS for this site."),
	}
}

func (Site) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("app", App.Type).
			Ref("sites").
			Unique().
			Comment("Optional managed app this site proxies to (v1+)."),
	}
}

func (Site) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("enabled"),
	}
}