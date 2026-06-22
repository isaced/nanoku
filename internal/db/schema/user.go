package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// User is an admin account that can sign into the management UI.
// Passwords are stored as bcrypt hashes; plaintext is never persisted.
type User struct{ ent.Schema }

func (User) Mixin() []ent.Mixin {
	return []ent.Mixin{TimeMixin{}}
}

func (User) Fields() []ent.Field {
	return []ent.Field{
		field.String("username").
			Unique().
			Comment("Login name (case-sensitive)."),
		field.String("password_hash").
			Sensitive().
			Comment("bcrypt hash of the password."),
		field.String("role").
			Default("admin").
			Comment("Reserved for future RBAC; v1 always 'admin'."),
		field.Time("last_login_at").
			Optional().
			Nillable().
			Comment("When the user last successfully authenticated."),
	}
}

func (User) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("sessions", Session.Type),
	}
}