package backup

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

type recorder struct {
	mu     sync.Mutex
	events []string
	active int
	max    int
}

func (r *recorder) enter(e string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
	r.active++
	if r.active > r.max {
		r.max = r.active
	}
}

func (r *recorder) leave() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.active--
}

func testInventory() Inventory {
	return Inventory{Items: []Item{
		{Kind: KindRBD, Name: "media/jellyfin-config", Pool: "kubernetes", Image: "csi-vol-1"},
		{Kind: KindRGWMetadata, Name: "default"},
		{Kind: KindRGW, Name: "photos", Bucket: "photos"},
		{Kind: KindCephFS, Name: "local-drive", FS: "local-drive"},
		{Kind: KindPool, Name: "mystery"},
	}}
}

// runWorkflow runs CephBackup against mocked activities; failBackup and
// failVerify name items whose backup or verify fails.
func runWorkflow(t *testing.T, inv Inventory, in Input, failBackup, failVerify string, start time.Time) (*recorder, Summary, error) {
	t.Helper()
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.SetStartTime(start)
	env.RegisterActivity(&Activities{})
	rec := &recorder{}
	env.OnActivity("Discover", mock.Anything).Return(func(context.Context) (Inventory, error) {
		rec.enter("discover")
		rec.leave()
		return inv, nil
	})
	backup := func(kind string) func(context.Context, Item) (BackupResult, error) {
		return func(_ context.Context, it Item) (BackupResult, error) {
			rec.enter("backup " + kind + " " + it.Name)
			defer rec.leave()
			time.Sleep(5 * time.Millisecond)
			if it.Name == failBackup {
				return BackupResult{}, errors.New("boom")
			}
			return BackupResult{SnapshotID: "s-" + it.Name, Source: "/run/x/" + it.Name}, nil
		}
	}
	env.OnActivity("BackupRBD", mock.Anything, mock.Anything).Return(backup("rbd"))
	env.OnActivity("BackupRGWMetadata", mock.Anything, mock.Anything).Return(backup("rgw-metadata"))
	env.OnActivity("BackupBucket", mock.Anything, mock.Anything).Return(backup("rgw"))
	env.OnActivity("BackupCephFS", mock.Anything, mock.Anything).Return(backup("cephfs"))
	env.OnActivity("Verify", mock.Anything, mock.Anything).Return(func(_ context.Context, in VerifyInput) error {
		rec.enter("verify " + in.SnapshotID + " " + fmt.Sprint(in.Percent))
		defer rec.leave()
		if in.SnapshotID == "s-"+failVerify {
			return temporal.NewNonRetryableApplicationError("shrank", ErrTypeVerify, nil)
		}
		return nil
	})
	env.OnActivity("Maintenance", mock.Anything, mock.Anything).Return(func(_ context.Context, full bool) error {
		if full {
			rec.enter("maintenance full")
		} else {
			rec.enter("maintenance quick")
		}
		rec.leave()
		return nil
	})
	env.OnActivity("SyncStorj", mock.Anything).Return(func(context.Context) error { rec.enter("sync"); rec.leave(); return nil })
	env.OnActivity("VerifyStorj", mock.Anything).Return(func(context.Context) error { rec.enter("verify storj"); rec.leave(); return nil })

	env.ExecuteWorkflow(CephBackup, in)
	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	var sum Summary
	err := env.GetWorkflowError()
	if err == nil {
		env.GetWorkflowResult(&sum)
	} else {
		var app *temporal.ApplicationError
		if errors.As(err, &app) && app.HasDetails() {
			app.Details(&sum)
		}
	}
	return rec, sum, err
}

var monday = time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)

func TestCephBackupOrder(t *testing.T) {
	rec, sum, err := runWorkflow(t, testInventory(), Input{}, "", "", monday)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"discover",
		"backup rbd media/jellyfin-config", "verify s-media/jellyfin-config 10",
		"backup rgw-metadata default", "verify s-default 100",
		"backup rgw photos", "verify s-photos 10",
		"backup cephfs local-drive", "verify s-local-drive 10",
		"maintenance quick", "sync", "verify storj",
	}
	if !reflect.DeepEqual(rec.events, want) {
		t.Errorf("events\n got %q\nwant %q", rec.events, want)
	}
	if rec.max != 1 {
		t.Errorf("%d activities ran at once", rec.max)
	}
	if len(sum.Items) != 4 || sum.Items[0].SnapshotID != "s-media/jellyfin-config" || len(sum.Failures) != 0 {
		t.Errorf("summary %+v", sum)
	}
}

func TestCephBackupFailuresAggregate(t *testing.T) {
	inv := testInventory()
	inv.Errors = []string{"client.ceph-backup has no caps on pool other-rbd"}
	rec, sum, err := runWorkflow(t, inv, Input{}, "photos", "local-drive", monday)
	var app *temporal.ApplicationError
	if !errors.As(err, &app) || app.Type() != ErrTypeFailed {
		t.Fatalf("error %v", err)
	}
	// Every item and the repository steps still ran.
	for _, e := range []string{"backup cephfs local-drive", "maintenance quick", "sync", "verify storj"} {
		found := false
		for _, got := range rec.events {
			found = found || got == e
		}
		if !found {
			t.Errorf("%s did not run: %q", e, rec.events)
		}
	}
	for _, e := range rec.events {
		if e == "verify s-photos 10" {
			t.Error("verified a failed backup")
		}
	}
	if len(sum.Failures) != 3 {
		t.Errorf("failures %q", sum.Failures)
	}
	if !strings.Contains(app.Error(), "3 failed") || !strings.Contains(app.Error(), "rgw photos") ||
		!strings.Contains(app.Error(), "cephfs local-drive") || !strings.Contains(app.Error(), "other-rbd") {
		t.Errorf("message %q", app.Error())
	}
}

func TestCephBackupWeeklyFullMaintenance(t *testing.T) {
	sunday := monday.Add(-24 * time.Hour)
	rec, _, err := runWorkflow(t, Inventory{}, Input{}, "", "", sunday)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rec.events, []string{"discover", "maintenance full", "sync", "verify storj"}) {
		t.Errorf("events %q", rec.events)
	}
	no := false
	rec, _, _ = runWorkflow(t, Inventory{}, Input{FullMaintenance: &no}, "", "", sunday)
	if rec.events[1] != "maintenance quick" {
		t.Errorf("events %q", rec.events)
	}
}

// fakeSchedules is a ScheduleClient holding at most the one Schedule.
type fakeSchedules struct {
	client.ScheduleClient
	client.ScheduleHandle
	schedule *client.Schedule
	paused   bool
	updates  int
}

func (f *fakeSchedules) Create(_ context.Context, o client.ScheduleOptions) (client.ScheduleHandle, error) {
	if f.schedule != nil {
		return nil, temporal.ErrScheduleAlreadyRunning
	}
	// As described back: the workflow by its type name, the calendar's
	// defaults spelled out.
	action := *o.Action.(*client.ScheduleWorkflowAction)
	spec := o.Spec
	spec.Calendars = []client.ScheduleCalendarSpec{{
		Second:     []client.ScheduleRange{{Start: 0, End: 0, Step: 1}},
		Minute:     []client.ScheduleRange{{Start: 0, End: 0, Step: 1}},
		Hour:       []client.ScheduleRange{{Start: 10, End: 10, Step: 1}},
		DayOfMonth: []client.ScheduleRange{{Start: 1, End: 31, Step: 1}},
	}}
	f.schedule = &client.Schedule{Spec: &spec, Action: &action,
		Policy: &client.SchedulePolicies{Overlap: o.Overlap}}
	f.paused = o.Paused
	return f, nil
}

func (f *fakeSchedules) GetHandle(context.Context, string) client.ScheduleHandle { return f }

func (f *fakeSchedules) Update(_ context.Context, o client.ScheduleUpdateOptions) error {
	u, err := o.DoUpdate(client.ScheduleUpdateInput{Description: client.ScheduleDescription{Schedule: *f.schedule}})
	if errors.Is(err, temporal.ErrSkipScheduleUpdate) {
		return nil
	}
	if err != nil {
		return err
	}
	f.updates++
	f.schedule = u.Schedule
	return nil
}

func TestEnsureSchedule(t *testing.T) {
	ctx := context.Background()
	f := &fakeSchedules{}
	if err := EnsureSchedule(ctx, f); err != nil {
		t.Fatal(err)
	}
	if !f.paused {
		t.Error("new schedule not paused")
	}
	a := f.schedule.Action.(*client.ScheduleWorkflowAction)
	if a.Workflow != "CephBackup" || a.TaskQueue != TaskQueue || f.schedule.Spec.TimeZoneName != "UTC" {
		t.Errorf("action %+v", a)
	}
	// Unpaused by hand; a restart leaves it so, and the same spec alone.
	f.paused = false
	if err := EnsureSchedule(ctx, f); err != nil {
		t.Fatal(err)
	}
	if f.paused || f.updates != 0 {
		t.Errorf("paused %v, updates %d", f.paused, f.updates)
	}
	// A schedule changed by hand is brought back.
	f.schedule.Spec.Calendars[0].Hour = []client.ScheduleRange{{Start: 3}}
	if err := EnsureSchedule(ctx, f); err != nil {
		t.Fatal(err)
	}
	if f.updates != 1 || f.schedule.Spec.Calendars[0].Hour[0].Start != 10 || f.paused {
		t.Errorf("updates %d, spec %+v", f.updates, f.schedule.Spec)
	}
}
