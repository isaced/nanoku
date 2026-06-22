package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// Session is a server-side login session identified by an opaque random
// token (the cookie value). bcrypt-style: token is high-entropy random,
// only its sha256 is stored in the DB; lookup is by token hash.
type Session struct{ ent.Schema }

func (Session) Fields() []ent.Field {
	return []ent.Field{
		// Hash of the random session token. The cookie carries the raw token;
		// the server hashes it and looks up by hash so a DB leak does not
		// let an attacker forge valid cookies.
		field.String("token_hash").
			Unique().
			MaxLen(64).
			Comment("SHA-256 of the cookie value (hex). 32 random bytes → 64 hex chars."),
		field.Time("created_at").
			Immutable(),
		field.Time("expires_at").
			Comment("Absolute expiry. Renewed (sliding) on each authenticated request."),
		field.Time("last_seen").
			Comment("Last request time, used to compute sliding expiry."),
	}
}

func (Session) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).
			Ref("sessions").
			Unique().
			Required(),
	}
}