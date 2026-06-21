// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"fmt"
	"strings"
	"testing"

	runnerv1 "gitea.dev/actions-proto-go/runner/v1"
	"gitea.dev/models/db"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/actions/jobparser"
	"gitea.dev/modules/timeutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestMakeTaskStepDisplayName(t *testing.T) {
	tests := []struct {
		name     string
		jobStep  *jobparser.Step
		expected string
	}{
		{
			name: "explicit name",
			jobStep: &jobparser.Step{
				Name: "Test Step",
			},
			expected: "Test Step",
		},
		{
			name: "uses step",
			jobStep: &jobparser.Step{
				Uses: "actions/checkout@v4",
			},
			expected: "Run actions/checkout@v4",
		},
		{
			name: "single-line run",
			jobStep: &jobparser.Step{
				Run: "echo hello",
			},
			expected: "Run echo hello",
		},
		{
			name: "multi-line run block scalar",
			jobStep: &jobparser.Step{
				Run: "\n  echo hello  \r\n  echo world  \n  ",
			},
			expected: "Run echo hello",
		},
		{
			name: "fallback to id",
			jobStep: &jobparser.Step{
				ID: "step-id",
			},
			expected: "Run step-id",
		},
		{
			name: "very long name truncated",
			jobStep: &jobparser.Step{
				Name: strings.Repeat("a", 300),
			},
			expected: strings.Repeat("a", 252) + "…",
		},
		{
			name: "very long run truncated",
			jobStep: &jobparser.Step{
				Run: strings.Repeat("a", 300),
			},
			expected: "Run " + strings.Repeat("a", 248) + "…",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := makeTaskStepDisplayName(tt.jobStep, 255)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTaskCancellingFinalizesToCancelled(t *testing.T) {
	newRunningTask := func(t *testing.T) (*ActionTask, *ActionRunJob) {
		t.Helper()

		run := &ActionRun{
			Title:         "cancelling-test-run",
			RepoID:        1,
			OwnerID:       2,
			WorkflowID:    "test.yaml",
			Index:         999,
			TriggerUserID: 2,
			Ref:           "refs/heads/master",
			CommitSHA:     "c2d72f548424103f01ee1dc02889c1e2bff816b0",
			Event:         "push",
			TriggerEvent:  "push",
			Status:        StatusRunning,
			Started:       timeutil.TimeStampNow(),
		}
		require.NoError(t, db.Insert(t.Context(), run))

		job := &ActionRunJob{
			RunID:     run.ID,
			RepoID:    run.RepoID,
			OwnerID:   run.OwnerID,
			CommitSHA: run.CommitSHA,
			Name:      "cancelling-finalization-job",
			Attempt:   1,
			JobID:     "cancelling-finalization-job",
			Status:    StatusRunning,
		}
		require.NoError(t, db.Insert(t.Context(), job))

		runner := &ActionRunner{
			UUID:                 "runner-cancelling-supported",
			Name:                 "runner-cancelling-supported",
			HasCancellingSupport: true,
		}
		require.NoError(t, db.Insert(t.Context(), runner))

		task := &ActionTask{
			JobID:     job.ID,
			Attempt:   1,
			RunnerID:  runner.ID,
			Status:    StatusRunning,
			Started:   timeutil.TimeStampNow(),
			RepoID:    run.RepoID,
			OwnerID:   run.OwnerID,
			CommitSHA: run.CommitSHA,
		}
		require.NoError(t, db.Insert(t.Context(), task))

		job.TaskID = task.ID
		_, err := UpdateRunJob(t.Context(), job, nil, "task_id")
		require.NoError(t, err)

		return task, job
	}

	testResult := func(t *testing.T, result runnerv1.Result) {
		t.Helper()
		require.NoError(t, unittest.PrepareTestDatabase())

		task, job := newRunningTask(t)
		require.NoError(t, StopTask(t.Context(), task.ID, StatusCancelling))

		taskAfterStop := unittest.AssertExistsAndLoadBean(t, &ActionTask{ID: task.ID})
		assert.Equal(t, StatusCancelling, taskAfterStop.Status)

		updatedTask, err := UpdateTaskByState(t.Context(), task.RunnerID, &runnerv1.TaskState{
			Id:        task.ID,
			Result:    result,
			StoppedAt: timestamppb.Now(),
		})
		require.NoError(t, err)
		assert.Equal(t, StatusCancelled, updatedTask.Status)

		taskAfterUpdate := unittest.AssertExistsAndLoadBean(t, &ActionTask{ID: task.ID})
		assert.Equal(t, StatusCancelled, taskAfterUpdate.Status)

		jobAfterUpdate := unittest.AssertExistsAndLoadBean(t, &ActionRunJob{ID: job.ID})
		assert.Equal(t, StatusCancelled, jobAfterUpdate.Status)
	}

	t.Run("runner reports success", func(t *testing.T) {
		testResult(t, runnerv1.Result_RESULT_SUCCESS)
	})

	t.Run("runner reports failure", func(t *testing.T) {
		testResult(t, runnerv1.Result_RESULT_FAILURE)
	})
}

func TestUpdateTaskByStateCreatesReusableWorkflowSteps(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	run := &ActionRun{
		Title:         "reusable-workflow-run",
		RepoID:        1,
		OwnerID:       2,
		WorkflowID:    "caller.yaml",
		Index:         998,
		TriggerUserID: 2,
		Ref:           "refs/heads/master",
		CommitSHA:     "c2d72f548424103f01ee1dc02889c1e2bff816b0",
		Event:         "push",
		TriggerEvent:  "push",
		Status:        StatusRunning,
		Started:       timeutil.TimeStampNow(),
	}
	require.NoError(t, db.Insert(t.Context(), run))

	// A `uses:` caller job has no steps of its own, so the task is created with
	// zero pre-seeded steps.
	job := &ActionRunJob{
		RunID:     run.ID,
		RepoID:    run.RepoID,
		OwnerID:   run.OwnerID,
		CommitSHA: run.CommitSHA,
		Name:      "reusable-caller-job",
		Attempt:   1,
		JobID:     "reusable-caller-job",
		Status:    StatusRunning,
	}
	require.NoError(t, db.Insert(t.Context(), job))

	runner := &ActionRunner{
		UUID: "runner-reusable-steps",
		Name: "runner-reusable-steps",
	}
	require.NoError(t, db.Insert(t.Context(), runner))

	task := &ActionTask{
		JobID:     job.ID,
		Attempt:   1,
		RunnerID:  runner.ID,
		Status:    StatusRunning,
		Started:   timeutil.TimeStampNow(),
		RepoID:    run.RepoID,
		OwnerID:   run.OwnerID,
		CommitSHA: run.CommitSHA,
	}
	require.NoError(t, db.Insert(t.Context(), task))

	job.TaskID = task.ID
	_, err := UpdateRunJob(t.Context(), job, nil, "task_id")
	require.NoError(t, err)

	// The runner reports steps that originate from the called reusable workflow.
	// None of them were pre-seeded at task creation.
	_, err = UpdateTaskByState(t.Context(), task.RunnerID, &runnerv1.TaskState{
		Id: task.ID,
		Steps: []*runnerv1.StepState{
			{
				Id:        0,
				Name:      "Run actions/checkout@v4",
				Result:    runnerv1.Result_RESULT_SUCCESS,
				LogIndex:  0,
				LogLength: 5,
				StartedAt: timestamppb.Now(),
				StoppedAt: timestamppb.Now(),
			},
			{
				// No name reported: must fall back to a generic label.
				Id:        1,
				LogIndex:  5,
				LogLength: 3,
				StartedAt: timestamppb.Now(),
			},
		},
	})
	require.NoError(t, err)

	steps, err := GetTaskStepsByTaskID(t.Context(), task.ID)
	require.NoError(t, err)
	require.Len(t, steps, 2, "reported reusable-workflow steps must be persisted")

	assert.Equal(t, int64(0), steps[0].Index)
	assert.Equal(t, "Run actions/checkout@v4", steps[0].Name)
	assert.Equal(t, StatusSuccess, steps[0].Status)
	assert.Equal(t, int64(5), steps[0].LogLength)

	assert.Equal(t, int64(1), steps[1].Index)
	assert.Equal(t, "Step 2", steps[1].Name)
	assert.Equal(t, StatusRunning, steps[1].Status)
	assert.Equal(t, int64(5), steps[1].LogIndex)
	assert.Equal(t, int64(3), steps[1].LogLength)
}

func TestStopTaskCancellingFallsBackForLegacyRunner(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	run := &ActionRun{
		Title:         "cancelling-test-run",
		RepoID:        1,
		OwnerID:       2,
		WorkflowID:    "test.yaml",
		Index:         999,
		TriggerUserID: 2,
		Ref:           "refs/heads/master",
		CommitSHA:     "c2d72f548424103f01ee1dc02889c1e2bff816b0",
		Event:         "push",
		TriggerEvent:  "push",
		Status:        StatusRunning,
		Started:       timeutil.TimeStampNow(),
	}
	require.NoError(t, db.Insert(t.Context(), run))

	job := &ActionRunJob{
		RunID:     run.ID,
		RepoID:    run.RepoID,
		OwnerID:   run.OwnerID,
		CommitSHA: run.CommitSHA,
		Name:      "legacy-cancelling-job",
		Attempt:   1,
		JobID:     "legacy-cancelling-job",
		Status:    StatusRunning,
	}
	require.NoError(t, db.Insert(t.Context(), job))

	runner := &ActionRunner{
		UUID:                 "runner-legacy-no-cancelling",
		Name:                 "runner-legacy-no-cancelling",
		HasCancellingSupport: false,
	}
	require.NoError(t, db.Insert(t.Context(), runner))

	task := &ActionTask{
		JobID:     job.ID,
		Attempt:   1,
		RunnerID:  runner.ID,
		Status:    StatusRunning,
		Started:   timeutil.TimeStampNow(),
		RepoID:    run.RepoID,
		OwnerID:   run.OwnerID,
		CommitSHA: run.CommitSHA,
	}
	require.NoError(t, db.Insert(t.Context(), task))

	job.TaskID = task.ID
	_, err := UpdateRunJob(t.Context(), job, nil, "task_id")
	require.NoError(t, err)

	require.NoError(t, StopTask(t.Context(), task.ID, StatusCancelling))

	taskAfterStop := unittest.AssertExistsAndLoadBean(t, &ActionTask{ID: task.ID})
	assert.Equal(t, StatusCancelled, taskAfterStop.Status)
	assert.NotZero(t, taskAfterStop.Stopped)

	jobAfterStop := unittest.AssertExistsAndLoadBean(t, &ActionRunJob{ID: job.ID})
	assert.Equal(t, StatusCancelled, jobAfterStop.Status)
	assert.NotZero(t, jobAfterStop.Stopped)
}

func TestStopTaskCancellingFallsBackForMissingRunner(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	run := &ActionRun{
		Title:         "cancelling-test-run",
		RepoID:        1,
		OwnerID:       2,
		WorkflowID:    "test.yaml",
		Index:         999,
		TriggerUserID: 2,
		Ref:           "refs/heads/master",
		CommitSHA:     "c2d72f548424103f01ee1dc02889c1e2bff816b0",
		Event:         "push",
		TriggerEvent:  "push",
		Status:        StatusRunning,
		Started:       timeutil.TimeStampNow(),
	}
	require.NoError(t, db.Insert(t.Context(), run))

	job := &ActionRunJob{
		RunID:     run.ID,
		RepoID:    run.RepoID,
		OwnerID:   run.OwnerID,
		CommitSHA: run.CommitSHA,
		Name:      "missing-runner-cancelling-job",
		Attempt:   1,
		JobID:     "missing-runner-cancelling-job",
		Status:    StatusRunning,
	}
	require.NoError(t, db.Insert(t.Context(), job))

	runner := &ActionRunner{
		UUID:                 "runner-cleaned-up-before-cancel",
		Name:                 "runner-cleaned-up-before-cancel",
		HasCancellingSupport: true,
	}
	require.NoError(t, db.Insert(t.Context(), runner))

	task := &ActionTask{
		JobID:     job.ID,
		Attempt:   1,
		RunnerID:  runner.ID,
		Status:    StatusRunning,
		Started:   timeutil.TimeStampNow(),
		RepoID:    run.RepoID,
		OwnerID:   run.OwnerID,
		CommitSHA: run.CommitSHA,
	}
	require.NoError(t, db.Insert(t.Context(), task))

	job.TaskID = task.ID
	_, err := UpdateRunJob(t.Context(), job, nil, "task_id")
	require.NoError(t, err)

	_, err = db.DeleteByID[ActionRunner](t.Context(), runner.ID)
	require.NoError(t, err)

	require.NoError(t, StopTask(t.Context(), task.ID, StatusCancelling))

	taskAfterStop := unittest.AssertExistsAndLoadBean(t, &ActionTask{ID: task.ID})
	assert.Equal(t, StatusCancelled, taskAfterStop.Status)
	assert.NotZero(t, taskAfterStop.Stopped)

	jobAfterStop := unittest.AssertExistsAndLoadBean(t, &ActionRunJob{ID: job.ID})
	assert.Equal(t, StatusCancelled, jobAfterStop.Status)
	assert.NotZero(t, jobAfterStop.Stopped)
}

// newRunningReusableTask creates a run/job/runner/task in the running state with
// no pre-seeded steps (the shape of a workflow_call caller job).
func newRunningReusableTask(t *testing.T) *ActionTask {
	t.Helper()
	run := &ActionRun{
		Title: "reusable-run", RepoID: 1, OwnerID: 2, WorkflowID: "caller.yaml",
		Index: 997, TriggerUserID: 2, Ref: "refs/heads/master",
		CommitSHA: "c2d72f548424103f01ee1dc02889c1e2bff816b0",
		Event:     "push", TriggerEvent: "push", Status: StatusRunning, Started: timeutil.TimeStampNow(),
	}
	require.NoError(t, db.Insert(t.Context(), run))
	job := &ActionRunJob{
		RunID: run.ID, RepoID: run.RepoID, OwnerID: run.OwnerID, CommitSHA: run.CommitSHA,
		Name: "caller", Attempt: 1, JobID: "caller", Status: StatusRunning,
	}
	require.NoError(t, db.Insert(t.Context(), job))
	runner := &ActionRunner{UUID: "runner-" + t.Name(), Name: "runner-" + t.Name()}
	require.NoError(t, db.Insert(t.Context(), runner))
	task := &ActionTask{
		JobID: job.ID, Attempt: 1, RunnerID: runner.ID, Status: StatusRunning,
		Started: timeutil.TimeStampNow(), RepoID: run.RepoID, OwnerID: run.OwnerID, CommitSHA: run.CommitSHA,
	}
	require.NoError(t, db.Insert(t.Context(), task))
	job.TaskID = task.ID
	_, err := UpdateRunJob(t.Context(), job, nil, "task_id")
	require.NoError(t, err)
	return task
}

// TestUpdateTaskByStateNormalJobInsertsNoExtraSteps verifies that for an
// ordinary job whose steps were pre-seeded, re-reporting those same step
// indexes never inserts duplicate rows (the highest-blast-radius guarantee).
func TestUpdateTaskByStateNormalJobInsertsNoExtraSteps(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	task := newRunningReusableTask(t)

	// Pre-seed two steps, as CreateTaskForRunner does for a job with steps.
	for i := range 2 {
		require.NoError(t, db.Insert(t.Context(), &ActionTaskStep{
			Name: fmt.Sprintf("seeded-%d", i), TaskID: task.ID, Index: int64(i),
			RepoID: task.RepoID, Status: StatusWaiting,
		}))
	}

	_, err := UpdateTaskByState(t.Context(), task.RunnerID, &runnerv1.TaskState{
		Id: task.ID,
		Steps: []*runnerv1.StepState{
			{Id: 0, Result: runnerv1.Result_RESULT_SUCCESS, StartedAt: timestamppb.Now(), StoppedAt: timestamppb.Now()},
			{Id: 1, StartedAt: timestamppb.Now()},
		},
	})
	require.NoError(t, err)

	steps, err := GetTaskStepsByTaskID(t.Context(), task.ID)
	require.NoError(t, err)
	require.Len(t, steps, 2, "re-reporting seeded indexes must not insert duplicates")
	assert.Equal(t, StatusSuccess, steps[0].Status)
	assert.Equal(t, StatusRunning, steps[1].Status)
}

// TestUpdateTaskByStateReusableStepsIdempotent verifies that repeated state
// reports (heartbeats) neither duplicate the dynamically-inserted steps nor
// drop status transitions on them.
func TestUpdateTaskByStateReusableStepsIdempotent(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	task := newRunningReusableTask(t)

	// First report: two inner steps, both running (no final task result).
	_, err := UpdateTaskByState(t.Context(), task.RunnerID, &runnerv1.TaskState{
		Id: task.ID,
		Steps: []*runnerv1.StepState{
			{Id: 0, Name: "build", LogLength: 4, StartedAt: timestamppb.Now()},
			{Id: 1, Name: "test", LogIndex: 4, StartedAt: timestamppb.Now()},
		},
	})
	require.NoError(t, err)

	// Second report (heartbeat): same steps, step 0 now finished.
	_, err = UpdateTaskByState(t.Context(), task.RunnerID, &runnerv1.TaskState{
		Id: task.ID,
		Steps: []*runnerv1.StepState{
			{Id: 0, Name: "build", Result: runnerv1.Result_RESULT_SUCCESS, LogLength: 4, StartedAt: timestamppb.Now(), StoppedAt: timestamppb.Now()},
			{Id: 1, Name: "test", LogIndex: 4, StartedAt: timestamppb.Now()},
		},
	})
	require.NoError(t, err)

	steps, err := GetTaskStepsByTaskID(t.Context(), task.ID)
	require.NoError(t, err)
	require.Len(t, steps, 2, "repeated reports must not duplicate dynamic steps")
	assert.Equal(t, StatusSuccess, steps[0].Status, "status transition must be applied on re-report")
	assert.Equal(t, StatusRunning, steps[1].Status)
}
