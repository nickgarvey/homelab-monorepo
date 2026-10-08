// Package authz maps client-certificate identities to Temporal roles: a claim
// mapper that names the caller, and an authorizer that looks the name up in a
// table and hands the decision to Temporal's default authorizer.
package authz

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"go.temporal.io/server/common/authorization"
	"gopkg.in/yaml.v3"
)

// Table maps a subject (a certificate's URI SAN, e.g.
// spiffe://home.garvey.sh/ns/orders/sa/orders-worker) to its roles. A subject
// that is not listed gets no roles, so every API but the health checks is
// denied to it.
//
//	subjects:
//	  spiffe://home.garvey.sh/ns/temporal/sa/temporal-ui:
//	    system: admin
//	  spiffe://home.garvey.sh/ns/orders/sa/orders-worker:
//	    namespaces:
//	      orders: write
//
// Roles are Temporal's: read, write, worker, admin. They are levels, not a set
// to combine: the default authorizer allows a call when the role is at least
// the one the API needs (read-only APIs need read, others write, admin APIs
// admin). Workers need write; worker on its own allows nothing.
type Table struct {
	subjects map[string]roles
}

type roles struct {
	system     authorization.Role
	namespaces map[string]authorization.Role
}

type tableFile struct {
	Subjects map[string]struct {
		System     string            `yaml:"system"`
		Namespaces map[string]string `yaml:"namespaces"`
	} `yaml:"subjects"`
}

// LoadTable reads a table from a YAML file. Unknown fields and unknown role
// names are errors, so a typo cannot silently grant or withhold access.
func LoadTable(path string) (*Table, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	var tf tableFile
	if err := dec.Decode(&tf); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	t := &Table{subjects: map[string]roles{}}
	for subject, entry := range tf.Subjects {
		r := roles{namespaces: map[string]authorization.Role{}}
		if entry.System != "" {
			if r.system, err = parseRole(entry.System); err != nil {
				return nil, fmt.Errorf("%s: subject %s: system: %w", path, subject, err)
			}
		}
		for ns, role := range entry.Namespaces {
			if r.namespaces[ns], err = parseRole(role); err != nil {
				return nil, fmt.Errorf("%s: subject %s: namespace %s: %w", path, subject, ns, err)
			}
		}
		t.subjects[subject] = r
	}
	return t, nil
}

func parseRole(s string) (authorization.Role, error) {
	switch strings.ToLower(s) {
	case "read":
		return authorization.RoleReader, nil
	case "write":
		return authorization.RoleWriter, nil
	case "worker":
		return authorization.RoleWorker, nil
	case "admin":
		return authorization.RoleAdmin, nil
	}
	return authorization.RoleUndefined, fmt.Errorf("unknown role %q (want read, write, worker or admin)", s)
}

// Subjects lists the table's subjects, sorted, for logging.
func (t *Table) Subjects() []string {
	out := make([]string, 0, len(t.subjects))
	for s := range t.subjects {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// claimsFor returns claims carrying the subject's roles; an unlisted subject
// gets claims with no roles.
func (t *Table) claimsFor(subject string) *authorization.Claims {
	c := &authorization.Claims{Subject: subject}
	r, ok := t.subjects[subject]
	if !ok {
		return c
	}
	c.System = r.system
	if len(r.namespaces) > 0 {
		c.Namespaces = make(map[string]authorization.Role, len(r.namespaces))
		for ns, role := range r.namespaces {
			c.Namespaces[ns] = role
		}
	}
	return c
}
