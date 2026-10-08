package authz

import (
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/server/common/authorization"
)

// source records where a caller's identity came from, in Claims.Extensions.
// The authorizer looks up the table only for certificate identities, so a JWT
// whose sub happens to equal a listed SPIFFE ID cannot borrow its roles.
type source int

const (
	sourceCertificate source = iota + 1
	sourceJWT
)

// SystemScope is the namespace name a token's permissions use for every
// namespace (Temporal's system role), e.g. the group temporal-system-admin
// parsed by permissionsRegex. Temporal's own JWT mapper reserves the name
// temporal-system for that, which would make the group
// temporal-temporal-system-admin; this mapper accepts system instead, so a
// Temporal namespace named system cannot be granted by token.
const SystemScope = "system"

// ClaimMapper names the caller and makes no role decisions. With a bearer
// token and a JWT mapper configured, the JWT mapper decides (its claims carry
// the token's permissions, with SystemScope folded into the system role). Otherwise the caller is the single URI SAN of its
// verified client certificate. A caller with neither gets no claims, which the
// default authorizer denies everything except health checks.
type ClaimMapper struct {
	jwt authorization.ClaimMapper // nil: bearer tokens are ignored
}

var _ authorization.ClaimMapper = (*ClaimMapper)(nil)

// NewClaimMapper returns a mapper that accepts bearer tokens only if jwt is
// non-nil.
func NewClaimMapper(jwt authorization.ClaimMapper) *ClaimMapper {
	return &ClaimMapper{jwt: jwt}
}

func (m *ClaimMapper) GetClaims(info *authorization.AuthInfo) (*authorization.Claims, error) {
	if info.AuthToken != "" && m.jwt != nil {
		claims, err := m.jwt.GetClaims(info)
		if err != nil {
			return nil, err
		}
		if claims != nil {
			claims.Extensions = sourceJWT
			if role, ok := claims.Namespaces[SystemScope]; ok {
				claims.System |= role
				delete(claims.Namespaces, SystemScope)
			}
		}
		return claims, nil
	}

	// VerifiedChains, not PeerCertificates: only a certificate that chained to
	// a configured client CA names anyone. With requireClientAuth Temporal
	// verifies every client certificate, so this is a second check, not the
	// only one.
	if info.TLSConnection == nil || len(info.TLSConnection.State.VerifiedChains) == 0 {
		return nil, nil
	}
	leaf := info.TLSConnection.State.VerifiedChains[0][0]
	if len(leaf.URIs) != 1 {
		return nil, serviceerror.NewPermissionDenied("client certificate must carry exactly one URI SAN", "")
	}
	return &authorization.Claims{
		Subject:    leaf.URIs[0].String(),
		Extensions: sourceCertificate,
	}, nil
}
