package authz

import (
	"context"

	"go.temporal.io/server/common/authorization"
)

// Authorizer gives certificate identities the roles the table lists for them,
// then lets Temporal's default authorizer decide. Claims from a JWT already
// carry their roles and pass through unchanged.
type Authorizer struct {
	table *Table
	next  authorization.Authorizer
}

var _ authorization.Authorizer = (*Authorizer)(nil)

func NewAuthorizer(table *Table) *Authorizer {
	return &Authorizer{table: table, next: authorization.NewDefaultAuthorizer()}
}

func (a *Authorizer) Authorize(ctx context.Context, claims *authorization.Claims, target *authorization.CallTarget) (authorization.Result, error) {
	if claims != nil && claims.Extensions == sourceCertificate {
		claims = a.table.claimsFor(claims.Subject)
	}
	return a.next.Authorize(ctx, claims, target)
}
