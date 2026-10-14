// agents-matrix is the Matrix bridge for the agents (packages/agents/internal/
// matrix): an application service that Synapse pushes events to, which runs
// each message from an allowed user as a turn of the room's conversation and
// posts the reply as the agent. It also serves chat.DeliverActivity on its
// own task queue, which posts wake-up replies. With --habit-api it is also
// the admin chat, @habit-admin (internal/habitadmin), whose PostgREST token
// comes from agent-token for its JWT-SVID. It logs in to Temporal with its own SVID
// (k8s/manifests/agents/README.md, "The Matrix bridge") and to Synapse with
// the registration's as_token.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"maunium.net/go/mautrix/appservice"
	"maunium.net/go/mautrix/id"

	"homelab/agents/internal/chat"
	"homelab/agents/internal/desired"
	"homelab/agents/internal/habitadmin"
	"homelab/agents/internal/matrix"
	"homelab/agents/internal/temporalconn"
)

func main() {
	var (
		tc           temporalconn.Config
		registration string
		homeserver   string
		server       string
		port         uint
		allow        string
		state        string
		adm          adminFlags
	)
	tc.Flags(flag.CommandLine)
	flag.StringVar(&registration, "registration", "", "The application service registration (YAML), with as_token and hs_token")
	flag.StringVar(&homeserver, "homeserver-url", "https://matrix.garvey.sh", "Synapse's client-server API")
	flag.StringVar(&server, "server-name", "matrix.garvey.sh", "Synapse's server name")
	flag.UintVar(&port, "port", 8009, "Port Synapse pushes transactions to")
	flag.StringVar(&allow, "allow", "", "Comma-separated Matrix IDs the agents talk to")
	flag.StringVar(&state, "state", "", "Published desired state (desired-state.json of the agents-desired-state ConfigMap)")
	flag.StringVar(&adm.name, "admin", "habit-admin", "Localpart of the admin chat's user, in the registration's namespace")
	flag.StringVar(&adm.agent, "admin-agent", "habit-helper", "The agent type whose data and wake-ups the admin chat manages")
	flag.StringVar(&adm.api, "habit-api", "", "Habit Helper's PostgREST; empty turns the admin chat off")
	flag.StringVar(&adm.tokenURL, "agent-token", "https://agent-token.agents.svc.cluster.local/token", "agent-token's token endpoint")
	flag.StringVar(&adm.svid, "jwt-svid", "/run/spiffe/jwt_svid.token", "The JWT-SVID for agent-token, kept fresh by spiffe-helper")
	flag.StringVar(&adm.ca, "agent-token-ca", "/run/spiffe/bundle.pem", "CA bundle agent-token's certificate chains to")
	flag.Parse()
	if err := run(&tc, registration, homeserver, server, uint16(port), allow, state, &adm); err != nil {
		fmt.Fprintln(os.Stderr, "agents-matrix:", err)
		os.Exit(1)
	}
}

type adminFlags struct {
	name, agent, api, tokenURL, svid, ca string
}

func run(tc *temporalconn.Config, registration, homeserver, server string, port uint16, allow, state string, adm *adminFlags) error {
	if registration == "" || state == "" || allow == "" {
		return fmt.Errorf("--registration, --state and --allow are required")
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	reg, err := appservice.LoadRegistration(registration)
	if err != nil {
		return fmt.Errorf("registration: %w", err)
	}
	var users []*regexp.Regexp
	for _, ns := range reg.Namespaces.UserIDs {
		re, err := regexp.Compile("^(?:" + ns.Regex + ")$")
		if err != nil {
			return fmt.Errorf("registration: user namespace %q: %w", ns.Regex, err)
		}
		users = append(users, re)
	}
	allowed := map[id.UserID]bool{}
	for _, u := range strings.Split(allow, ",") {
		u = strings.TrimSpace(u)
		if _, _, err := id.UserID(u).Parse(); err != nil {
			return fmt.Errorf("--allow: %q: %w", u, err)
		}
		allowed[id.UserID(u)] = true
	}

	as, err := appservice.CreateFull(appservice.CreateOpts{
		Registration:     reg,
		HomeserverDomain: server,
		HomeserverURL:    homeserver,
		// An empty host is every address; mautrix joins host and port
		// without brackets, so "::" would not parse.
		HostConfig: appservice.HostConfig{Hostname: "", Port: port},
	})
	if err != nil {
		return err
	}
	as.Log = zerolog.New(os.Stderr).Level(zerolog.WarnLevel).With().Timestamp().Logger()

	c, err := tc.Dial()
	if err != nil {
		return fmt.Errorf("temporal: %w", err)
	}
	defer c.Close()

	b := &matrix.Bridge{
		HS:      &matrix.Synapse{AS: as},
		Turns:   &matrix.TemporalTurns{Client: c},
		Server:  server,
		Allowed: allowed,
		// The ConfigMap is read afresh each time, so a type agents-apply
		// publishes is served without a restart, once the registration's
		// namespace has its user.
		Agents: func() (map[string]bool, error) {
			st, err := desired.Load(state)
			if err != nil {
				return nil, err
			}
			agents := map[string]bool{}
			for typ := range st.Types {
				user := string(id.NewUserID(typ, server))
				for _, re := range users {
					if re.MatchString(user) {
						agents[typ] = true
					}
				}
			}
			return agents, nil
		},
		Log: log,
	}
	if adm.api != "" {
		admin, err := newAdmin(adm, state, c, b)
		if err != nil {
			return fmt.Errorf("admin chat: %w", err)
		}
		b.Admin, b.AdminName = admin, adm.name
		user := string(id.NewUserID(adm.name, server))
		served := false
		for _, re := range users {
			served = served || re.MatchString(user)
		}
		if !served {
			return fmt.Errorf("--admin %s: %s is not in the registration's users namespace", adm.name, user)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go as.Start()
	if err := b.Start(ctx); err != nil {
		as.Stop()
		return err
	}
	w := worker.New(c, chat.DeliverTaskQueue, worker.Options{})
	w.RegisterActivityWithOptions(b.Deliver, activity.RegisterOptions{Name: chat.DeliverActivity})
	if err := w.Start(); err != nil {
		as.Stop()
		return fmt.Errorf("temporal worker: %w", err)
	}
	defer w.Stop()
	as.Ready = true
	agents, _ := b.Agents()
	log.Info("Bridging", "agents", desired.SortedKeys(agents), "allowed", allow)

	for {
		select {
		case evt := <-as.Events:
			b.Handle(ctx, evt)
		case <-ctx.Done():
			as.Stop()
			// Replies still running are lost if the pod goes before they
			// come; give them what the termination grace period allows.
			done := make(chan struct{})
			go func() { b.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(20 * time.Second):
				log.Warn("Stopping with turns still running; their replies will not be posted")
			}
			return nil
		}
	}
}

// newAdmin is the admin chat for the agent type adm.agent: its data in
// PostgREST, its wake-ups in Temporal, and its direct chat with each user.
func newAdmin(adm *adminFlags, state string, c client.Client, b *matrix.Bridge) (*habitadmin.Admin, error) {
	pem, err := os.ReadFile(adm.ca)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("%s: no certificates", adm.ca)
	}
	tok := &habitadmin.AgentToken{
		URL: adm.tokenURL, SVIDFile: adm.svid, Audience: "habit-data",
		HTTP: &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}},
	}
	return &habitadmin.Admin{
		Data:    &habitadmin.Store{URL: adm.api, Token: tok.Token, HTTP: &http.Client{Timeout: 30 * time.Second}},
		Wakeups: &habitadmin.TemporalWakeups{Client: c, Type: adm.agent},
		Conversation: func(ctx context.Context, user string) (string, error) {
			return b.ConversationWith(ctx, adm.agent, id.UserID(user))
		},
		Session: func() (string, int, error) {
			st, err := desired.Load(state)
			if err != nil {
				return "", 0, err
			}
			t, ok := st.Types[adm.agent]
			if !ok {
				return "", 0, fmt.Errorf("%s is not published", adm.agent)
			}
			return t.Session.Timezone, t.Session.DefaultWakeupSeconds, nil
		},
	}, nil
}
