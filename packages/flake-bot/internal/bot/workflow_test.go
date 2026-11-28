package bot

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

func newEnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	env.RegisterActivity(&Activities{})
	return env
}

var a *Activities

func okBuild(rev string) BuildResult {
	return BuildResult{Rev: rev, Targets: []TargetResult{{Kind: KindHost, Name: "talos", OutPath: "/nix/store/t"}}}
}

func appErrType(t *testing.T, err error) string {
	t.Helper()
	var ae *temporal.ApplicationError
	require.True(t, errors.As(err, &ae), "error %v", err)
	return ae.Type()
}

func TestUpdateRunLands(t *testing.T) {
	env := newEnv(t)
	env.OnActivity(a.PrepareUpdate, mock.Anything).Return(PrepareResult{Base: "base", OldBranch: "old", Head: "head", Inputs: []string{"nixpkgs"}}, nil)
	env.OnActivity(a.Build, mock.Anything, BuildRequest{Worktree: UpdateWorktree, Rev: "head"}).Return(okBuild("head"), nil)
	env.OnActivity(a.PushCeller, mock.Anything, []string{"/nix/store/t"}).Return(nil)
	env.OnActivity(a.PushBranch, mock.Anything, PushBranchRequest{Head: "head", OldBranch: "old"}).Return(nil)
	env.OnActivity(a.Land, mock.Anything, LandRequest{Base: "base", Head: "head"}).Return(LandResult{Landed: true}, nil)
	env.ExecuteWorkflow(UpdateRun)
	require.NoError(t, env.GetWorkflowError())
	var res UpdateResult
	require.NoError(t, env.GetWorkflowResult(&res))
	require.True(t, res.Pushed)
	require.True(t, res.Land.Landed)
}

func TestUpdateRunBuildFailureDoesNotLand(t *testing.T) {
	env := newEnv(t)
	b := BuildResult{Rev: "head", Targets: []TargetResult{
		{Kind: KindHost, Name: "talos", OutPath: "/nix/store/t"},
		{Kind: KindHost, Name: "skyforge", Error: "build failed"},
	}}
	env.OnActivity(a.PrepareUpdate, mock.Anything).Return(PrepareResult{Base: "base", Head: "head"}, nil)
	env.OnActivity(a.Build, mock.Anything, mock.Anything).Return(b, nil)
	env.OnActivity(a.PushCeller, mock.Anything, []string{"/nix/store/t"}).Return(nil)
	env.OnActivity(a.PushBranch, mock.Anything, mock.Anything).Return(nil)
	env.ExecuteWorkflow(UpdateRun)
	err := env.GetWorkflowError()
	require.Error(t, err)
	require.Equal(t, ErrTypeBuildFailed, appErrType(t, err))
	require.Contains(t, err.Error(), "host skyforge")
	env.AssertNotCalled(t, "Land", mock.Anything, mock.Anything)
}

func TestUpdateRunConflictPushesNothing(t *testing.T) {
	env := newEnv(t)
	env.OnActivity(a.PrepareUpdate, mock.Anything).Return(PrepareResult{Base: "base", OldBranch: "old", Head: "partial", Conflict: "patch does not apply: abc fix"}, nil)
	env.ExecuteWorkflow(UpdateRun)
	err := env.GetWorkflowError()
	require.Equal(t, ErrTypeConflict, appErrType(t, err))
	env.AssertNotCalled(t, "PushBranch", mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "Build", mock.Anything, mock.Anything)
}

func TestUpdateRunNothingNew(t *testing.T) {
	env := newEnv(t)
	env.OnActivity(a.PrepareUpdate, mock.Anything).Return(PrepareResult{Base: "base", OldBranch: "old", Head: "base"}, nil)
	env.OnActivity(a.PushBranch, mock.Anything, PushBranchRequest{Head: "base", OldBranch: "old"}).Return(nil)
	env.ExecuteWorkflow(UpdateRun)
	require.NoError(t, env.GetWorkflowError())
	env.AssertNotCalled(t, "Build", mock.Anything, mock.Anything)
}

func TestDevBuildSkipsBuiltCommit(t *testing.T) {
	env := newEnv(t)
	env.OnActivity(a.PrepareDev, mock.Anything).Return(DevResult{Rev: "r1", Subject: "s"}, nil)
	env.OnActivity(a.ReadManifest, mock.Anything).Return(Manifest{Commit: "r1"}, nil)
	env.ExecuteWorkflow(DevBuild)
	require.NoError(t, env.GetWorkflowError())
	var res DevBuildResult
	require.NoError(t, env.GetWorkflowResult(&res))
	require.True(t, res.Skipped)
}

func TestDevBuildPublishes(t *testing.T) {
	env := newEnv(t)
	dev := DevResult{Rev: "r2", Subject: "s"}
	env.OnActivity(a.PrepareDev, mock.Anything).Return(dev, nil)
	env.OnActivity(a.ReadManifest, mock.Anything).Return(Manifest{Commit: "r1"}, nil)
	env.OnActivity(a.Build, mock.Anything, BuildRequest{Worktree: DevWorktree, Rev: "r2"}).Return(okBuild("r2"), nil)
	env.OnActivity(a.PushCeller, mock.Anything, []string{"/nix/store/t"}).Return(nil)
	env.OnActivity(a.Publish, mock.Anything, PublishRequest{Dev: dev, Build: okBuild("r2")}).Return(Manifest{Commit: "r2"}, nil)
	env.ExecuteWorkflow(DevBuild)
	require.NoError(t, env.GetWorkflowError())
	var res DevBuildResult
	require.NoError(t, env.GetWorkflowResult(&res))
	require.Equal(t, "r2", res.Manifest.Commit)
}
