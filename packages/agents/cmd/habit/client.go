package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The sandbox reaches PostgREST through the OpenShell supervisor's proxy, but
// not as an HTTP proxy: the sandbox's egress is captured transparently (its
// DNS answers and TCP connections go to the supervisor), and the supervisor
// removes HTTP_PROXY and friends from sandbox processes. So the client dials
// the URL directly and ignores proxy variables. The supervisor puts the
// PostgREST token in the Authorization header, replacing any the request has,
// so the client sends none.

const defaultAPIURL = "http://postgrest.habit-helper.svc.cluster.local:3000"

type client struct {
	base string
	http *http.Client
}

func newClient(base string) *client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	return &client{
		base: strings.TrimRight(base, "/"),
		http: &http.Client{Transport: tr, Timeout: 30 * time.Second},
	}
}

// apiError is a refused request, from PostgREST or the sandbox proxy.
type apiError struct {
	Status  int
	Code    string `json:"code"`
	Message string `json:"message"`
	Details string `json:"details"`
	Hint    string `json:"hint"`
	Body    string
}

func (e *apiError) Error() string {
	switch {
	case e.Status == http.StatusUnauthorized:
		return fmt.Sprintf("PostgREST refused the request as unauthenticated (HTTP 401: %s). "+
			"The token comes from the sandbox supervisor, not from this CLI: the sandbox needs the "+
			"habit-data provider and the habit-helper agent type", e.msg())
	case e.Status == http.StatusBadGateway || e.Status == http.StatusServiceUnavailable || e.Status == http.StatusGatewayTimeout:
		return fmt.Sprintf("the sandbox proxy could not complete the request (HTTP %d: %s). "+
			"Either it got no token (the sandbox needs the habit-data provider and the habit-helper "+
			"agent type) or PostgREST is down; try again in a minute", e.Status, e.msg())
	case e.Status == http.StatusForbidden && e.Code == "":
		return fmt.Sprintf("the sandbox proxy refused the request (HTTP 403: %s)", e.msg())
	}
	return fmt.Sprintf("PostgREST: HTTP %d: %s", e.Status, e.msg())
}

func (e *apiError) msg() string {
	var parts []string
	for _, p := range []string{e.Message, e.Details, e.Hint} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		b := strings.TrimSpace(e.Body)
		if len(b) > 300 {
			b = b[:300] + "..."
		}
		if b == "" {
			b = http.StatusText(e.Status)
		}
		return b
	}
	return strings.Join(parts, "; ")
}

// do sends a request to PostgREST. query may be nil, body nil for none; out,
// if not nil, gets the JSON response.
func (c *client) do(ctx context.Context, method, path string, query url.Values, body any, prefer string, out any) error {
	u := c.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if prefer != "" {
		req.Header.Set("Prefer", prefer)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("cannot reach PostgREST at %s: %v. It is reachable only from a sandbox "+
			"with the habit-data provider (or set HABIT_API_URL)", c.base, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return fmt.Errorf("reading PostgREST's response: %v", err)
	}
	if resp.StatusCode >= 300 {
		e := &apiError{Status: resp.StatusCode, Body: string(data)}
		_ = json.Unmarshal(data, e)
		return e
	}
	if out != nil && len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("PostgREST's response is not what this CLI expects: %v", err)
		}
	}
	return nil
}

// PostgreSQL error codes PostgREST passes on.
const (
	pgUniqueViolation     = "23505"
	pgForeignKeyViolation = "23503"
	pgCheckViolation      = "23514"
)

func pgCode(err error) string {
	var e *apiError
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func eq(v string) string { return "eq." + v }
