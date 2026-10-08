package authz

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"go.temporal.io/server/common/authorization"
	"google.golang.org/grpc/credentials"
)

const (
	apiStart       = "/temporal.api.workflowservice.v1.WorkflowService/StartWorkflowExecution"
	apiDescribe    = "/temporal.api.workflowservice.v1.WorkflowService/DescribeWorkflowExecution"
	apiPoll        = "/temporal.api.workflowservice.v1.WorkflowService/PollWorkflowTaskQueue"
	apiRegisterNs  = "/temporal.api.workflowservice.v1.WorkflowService/RegisterNamespace"
	apiHealthCheck = "/grpc.health.v1.Health/Check"

	uiID     = "spiffe://home.garvey.sh/ns/temporal/sa/temporal-ui"
	workerID = "spiffe://home.garvey.sh/ns/orders/sa/orders-worker"
	readerID = "spiffe://home.garvey.sh/ns/orders/sa/orders-dashboard"
	otherID  = "spiffe://home.garvey.sh/ns/playwright/sa/default"
)

const tableYAML = `
subjects:
  ` + uiID + `:
    system: admin
  ` + workerID + `:
    namespaces:
      orders: write
  ` + readerID + `:
    namespaces:
      orders: read
`

func writeTable(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "table.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func loadTable(t *testing.T) *Table {
	t.Helper()
	table, err := LoadTable(writeTable(t, tableYAML))
	if err != nil {
		t.Fatal(err)
	}
	return table
}

// certInfo is AuthInfo for a client whose verified certificate has these URI SANs.
func certInfo(t *testing.T, uris ...string) *authorization.AuthInfo {
	t.Helper()
	leaf := &x509.Certificate{}
	for _, u := range uris {
		parsed, err := url.Parse(u)
		if err != nil {
			t.Fatal(err)
		}
		leaf.URIs = append(leaf.URIs, parsed)
	}
	state := tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{leaf},
		VerifiedChains:   [][]*x509.Certificate{{leaf}},
	}
	return &authorization.AuthInfo{TLSConnection: &credentials.TLSInfo{State: state}}
}

func TestClaimMapperCertificate(t *testing.T) {
	m := NewClaimMapper(nil)

	claims, err := m.GetClaims(certInfo(t, workerID))
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != workerID || claims.Extensions != sourceCertificate {
		t.Fatalf("claims = %+v, want subject %s from a certificate", claims, workerID)
	}
	if claims.System != 0 || claims.Namespaces != nil {
		t.Fatalf("claim mapper granted roles: %+v", claims)
	}

	for name, info := range map[string]*authorization.AuthInfo{
		"no URI SAN":  certInfo(t),
		"two URI SAN": certInfo(t, workerID, uiID),
	} {
		if _, err := m.GetClaims(info); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestClaimMapperIgnoresUnverifiedCertificate(t *testing.T) {
	info := certInfo(t, uiID)
	info.TLSConnection.State.VerifiedChains = nil
	claims, err := NewClaimMapper(nil).GetClaims(info)
	if err != nil || claims != nil {
		t.Fatalf("unverified certificate: claims %+v, err %v; want none", claims, err)
	}
}

func TestClaimMapperNoCredentials(t *testing.T) {
	claims, err := NewClaimMapper(nil).GetClaims(&authorization.AuthInfo{})
	if err != nil || claims != nil {
		t.Fatalf("no credentials: claims %+v, err %v; want none", claims, err)
	}
}

type fakeJWT struct{ claims *authorization.Claims }

func (f fakeJWT) GetClaims(*authorization.AuthInfo) (*authorization.Claims, error) {
	return f.claims, nil
}

func TestClaimMapperBearerToken(t *testing.T) {
	jwtClaims := &authorization.Claims{Subject: "user", Namespaces: map[string]authorization.Role{"orders": authorization.RoleReader}}
	info := certInfo(t, uiID)
	info.AuthToken = "Bearer x"

	// With a JWT mapper the token decides, even over a certificate.
	claims, err := NewClaimMapper(fakeJWT{jwtClaims}).GetClaims(info)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != "user" || claims.Extensions != sourceJWT {
		t.Fatalf("claims = %+v, want the JWT's", claims)
	}

	// Without one the token is ignored and the certificate names the caller.
	claims, err = NewClaimMapper(nil).GetClaims(info)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != uiID {
		t.Fatalf("claims = %+v, want the certificate's subject", claims)
	}
}

func TestClaimMapperSystemScope(t *testing.T) {
	// What the stock JWT mapper returns for the groups temporal-system-admin
	// and temporal-orders-read with the permissionsRegex in config-configmap.yaml.
	jwtClaims := &authorization.Claims{Subject: "user", Namespaces: map[string]authorization.Role{
		SystemScope: authorization.RoleAdmin,
		"orders":    authorization.RoleReader,
	}}
	info := &authorization.AuthInfo{AuthToken: "Bearer x"}
	claims, err := NewClaimMapper(fakeJWT{jwtClaims}).GetClaims(info)
	if err != nil {
		t.Fatal(err)
	}
	if claims.System != authorization.RoleAdmin {
		t.Fatalf("System = %v, want admin", claims.System)
	}
	if _, ok := claims.Namespaces[SystemScope]; ok || claims.Namespaces["orders"] != authorization.RoleReader {
		t.Fatalf("Namespaces = %v, want only orders: read", claims.Namespaces)
	}
	// And the default authorizer then allows cluster-scoped calls.
	if !authorize(t, NewAuthorizer(loadTable(t)), claims, apiRegisterNs, "") {
		t.Fatal("system admin from a token cannot register namespaces")
	}
}

func authorize(t *testing.T, a *Authorizer, claims *authorization.Claims, api, namespace string) bool {
	t.Helper()
	res, err := a.Authorize(context.Background(), claims, &authorization.CallTarget{APIName: api, Namespace: namespace})
	if err != nil {
		t.Fatal(err)
	}
	return res.Decision == authorization.DecisionAllow
}

func certClaims(subject string) *authorization.Claims {
	return &authorization.Claims{Subject: subject, Extensions: sourceCertificate}
}

func TestAuthorizerTable(t *testing.T) {
	a := NewAuthorizer(loadTable(t))
	cases := []struct {
		name      string
		claims    *authorization.Claims
		api, ns   string
		wantAllow bool
	}{
		{"worker polls its namespace", certClaims(workerID), apiPoll, "orders", true},
		{"worker starts in its namespace", certClaims(workerID), apiStart, "orders", true},
		{"worker confined to its namespace", certClaims(workerID), apiStart, "billing", false},
		{"worker cannot register namespaces", certClaims(workerID), apiRegisterNs, "", false},
		{"reader reads", certClaims(readerID), apiDescribe, "orders", true},
		{"reader cannot start", certClaims(readerID), apiStart, "orders", false},
		{"unlisted identity denied", certClaims(otherID), apiDescribe, "orders", false},
		{"system admin anywhere", certClaims(uiID), apiStart, "billing", true},
		{"system admin registers namespaces", certClaims(uiID), apiRegisterNs, "", true},
		{"no claims denied", nil, apiDescribe, "orders", false},
		{"health check without claims", nil, apiHealthCheck, "", true},
	}
	for _, c := range cases {
		if got := authorize(t, a, c.claims, c.api, c.ns); got != c.wantAllow {
			t.Errorf("%s: allow = %v, want %v", c.name, got, c.wantAllow)
		}
	}
}

func TestAuthorizerJWTSubjectCannotBorrowTableRoles(t *testing.T) {
	a := NewAuthorizer(loadTable(t))
	// A token whose sub equals a listed SPIFFE ID, with no permissions of its own.
	claims := &authorization.Claims{Subject: uiID, Extensions: sourceJWT}
	if authorize(t, a, claims, apiDescribe, "orders") {
		t.Fatal("JWT claims got the table's roles for their subject")
	}
	// A token's own permissions are honoured.
	claims = &authorization.Claims{Subject: "user", Extensions: sourceJWT,
		Namespaces: map[string]authorization.Role{"orders": authorization.RoleReader}}
	if !authorize(t, a, claims, apiDescribe, "orders") {
		t.Fatal("JWT permissions not honoured")
	}
}

func TestLoadTableRejectsMistakes(t *testing.T) {
	for name, content := range map[string]string{
		"unknown role":  "subjects:\n  " + workerID + ":\n    namespaces:\n      orders: writer\n",
		"unknown field": "subjects:\n  " + workerID + ":\n    namespace:\n      orders: write\n",
		"bad system":    "subjects:\n  " + uiID + ":\n    system: root\n",
	} {
		if _, err := LoadTable(writeTable(t, content)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
