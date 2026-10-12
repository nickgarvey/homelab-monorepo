// agents-chat talks to an agent conversation from a terminal: each line read
// is sent to the conversation's ChatSession as a message, with
// Update-with-Start, and the reply is printed. With -m it sends that one
// message and exits. On start it prints the conversation's pending wake-up.
//
// It connects to Temporal's internal frontend through the operator's
// break-glass port-forward ("Ports" in k8s/manifests/temporal/README.md),
// so it has no identity of its own. It is temporary, for talking to agents
// until the Matrix transport exists (agents/README.md, "agents-chat"); the
// flake stops evaluating it after its deadline in agents/outputs.nix.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/log"

	"homelab/agents/internal/chat"
	"homelab/agents/internal/session"
)

func main() {
	address := flag.String("address", "127.0.0.1:7236", "Temporal internal frontend, host:port (the break-glass port-forward)")
	namespace := flag.String("namespace", "agents", "Temporal namespace")
	message := flag.String("m", "", "Send this one message, print the reply and exit")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: agents-chat [flags] <type> <conversation>\n\n")
		flag.PrintDefaults()
	}
	// Flags may come before or after the arguments.
	flag.Parse()
	var args []string
	for flag.NArg() > 0 {
		args = append(args, flag.Arg(0))
		_ = flag.CommandLine.Parse(flag.Args()[1:])
	}
	if len(args) != 2 {
		flag.Usage()
		os.Exit(2)
	}
	typ, conv := args[0], args[1]
	if err := session.ValidConversation(conv); err != nil {
		fmt.Fprintln(os.Stderr, "agents-chat:", err)
		os.Exit(2)
	}
	oneShot := false
	flag.Visit(func(f *flag.Flag) { oneShot = oneShot || f.Name == "m" })

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	c, err := client.Dial(client.Options{
		HostPort:  *address,
		Namespace: *namespace,
		Logger:    log.NewStructuredLogger(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "agents-chat: %v\n(is `kubectl -n temporal port-forward deploy/temporal 7236:7236` running?)\n", err)
		os.Exit(1)
	}
	defer c.Close()

	ch := &chatter{c: c, typ: typ, conv: conv}
	ch.showWakeup(ctx)
	if oneShot {
		reply, err := ch.send(ctx, *message)
		if err != nil {
			fmt.Fprintln(os.Stderr, "agents-chat:", err)
			os.Exit(1)
		}
		fmt.Println(reply)
		return
	}
	if err := ch.interactive(ctx, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "agents-chat:", err)
		os.Exit(1)
	}
}

type chatter struct {
	c         client.Client
	typ, conv string
}

// showWakeup prints the conversation's pending wake-up to stderr, or that it
// has none.
func (ch *chatter) showWakeup(ctx context.Context) {
	st, err := chat.QueryWakeup(ctx, ch.c, session.Conversation{Type: ch.typ, Conversation: ch.conv})
	switch {
	case err != nil:
		fmt.Fprintln(os.Stderr, "[next wake-up] unknown:", err)
	case st == nil:
		fmt.Fprintln(os.Stderr, "[next wake-up] none")
	default:
		fmt.Fprintln(os.Stderr, describeWakeup(st, time.Now()))
	}
}

// describeWakeup is a WakeupStatus as one line, in local time.
func describeWakeup(st *session.WakeupStatus, now time.Time) string {
	var b strings.Builder
	if st.Pending == nil {
		fmt.Fprintf(&b, "[next wake-up] a wake-up turn is running; without a request the next comes %s after it",
			time.Duration(st.NextFloorSeconds)*time.Second)
	} else {
		at := st.Pending.At.Local()
		fmt.Fprintf(&b, "[next wake-up] %s (in %s). Note: %s",
			at.Format("2006-01-02 15:04 MST"), at.Sub(now).Round(time.Minute), st.Pending.Note)
	}
	if st.Rejected != "" {
		fmt.Fprintf(&b, "\n[wake-up request refused] %s", st.Rejected)
	}
	return b.String()
}

// interactive sends each non-empty line of in and prints the reply, until
// end of input. A failed turn is reported and the next line read.
func (ch *chatter) interactive(ctx context.Context, in io.Reader, out io.Writer) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64*1024), chat.MaxMessageBytes+1)
	for {
		fmt.Fprint(out, "> ")
		if !sc.Scan() {
			fmt.Fprintln(out)
			return sc.Err()
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		reply, err := ch.send(ctx, line)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "agents-chat:", err)
			continue
		}
		fmt.Fprintf(out, "%s\n\n", reply)
	}
}

// send runs one turn: the message Update, with Update-with-Start, waiting
// for the reply.
func (ch *chatter) send(ctx context.Context, text string) (string, error) {
	if text == "" {
		return "", errors.New("empty message")
	}
	start := ch.c.NewWithStartWorkflowOperation(client.StartWorkflowOptions{
		ID:                       chat.WorkflowID(ch.typ, ch.conv),
		TaskQueue:                chat.TaskQueue,
		WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
	}, chat.ChatSession, chat.ChatInput{Type: ch.typ, Conversation: ch.conv})
	h, err := ch.c.UpdateWithStartWorkflow(ctx, client.UpdateWithStartWorkflowOptions{
		StartWorkflowOperation: start,
		UpdateOptions: client.UpdateWorkflowOptions{
			UpdateName:   chat.MessageUpdate,
			Args:         []any{chat.Message{Text: text}},
			WaitForStage: client.WorkflowUpdateStageCompleted,
		},
	})
	if err != nil {
		return "", err
	}
	var reply chat.Reply
	if err := h.Get(ctx, &reply); err != nil {
		return "", err
	}
	return reply.Text, nil
}
