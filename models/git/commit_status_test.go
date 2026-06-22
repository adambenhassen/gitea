// Copyright 2017 Gitea. All rights reserved.
// SPDX-License-Identifier: MIT

package git_test

import (
	"fmt"
	"testing"
	"time"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	git_model "gitea.dev/models/git"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/commitstatus"
	"gitea.dev/modules/gitrepo"

	"github.com/stretchr/testify/assert"
)

func TestGetCommitStatuses(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	repo1 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})

	sha1 := "1234123412341234123412341234123412341234" // the mocked commit ID in test fixtures

	statuses, maxResults, err := db.FindAndCount[git_model.CommitStatus](t.Context(), &git_model.CommitStatusOptions{
		ListOptions: db.ListOptions{Page: 1, PageSize: 50},
		RepoID:      repo1.ID,
		SHA:         sha1,
	})
	assert.NoError(t, err)
	assert.Equal(t, 5, int(maxResults))
	assert.Len(t, statuses, 5)

	assert.Equal(t, "ci/awesomeness", statuses[0].Context)
	assert.Equal(t, commitstatus.CommitStatusPending, statuses[0].State)
	assert.Equal(t, "https://try.gitea.io/api/v1/repos/user2/repo1/statuses/1234123412341234123412341234123412341234", statuses[0].APIURL(t.Context()))

	assert.Equal(t, "cov/awesomeness", statuses[1].Context)
	assert.Equal(t, commitstatus.CommitStatusWarning, statuses[1].State)
	assert.Equal(t, "https://try.gitea.io/api/v1/repos/user2/repo1/statuses/1234123412341234123412341234123412341234", statuses[1].APIURL(t.Context()))

	assert.Equal(t, "cov/awesomeness", statuses[2].Context)
	assert.Equal(t, commitstatus.CommitStatusSuccess, statuses[2].State)
	assert.Equal(t, "https://try.gitea.io/api/v1/repos/user2/repo1/statuses/1234123412341234123412341234123412341234", statuses[2].APIURL(t.Context()))

	assert.Equal(t, "ci/awesomeness", statuses[3].Context)
	assert.Equal(t, commitstatus.CommitStatusFailure, statuses[3].State)
	assert.Equal(t, "https://try.gitea.io/api/v1/repos/user2/repo1/statuses/1234123412341234123412341234123412341234", statuses[3].APIURL(t.Context()))

	assert.Equal(t, "deploy/awesomeness", statuses[4].Context)
	assert.Equal(t, commitstatus.CommitStatusError, statuses[4].State)
	assert.Equal(t, "https://try.gitea.io/api/v1/repos/user2/repo1/statuses/1234123412341234123412341234123412341234", statuses[4].APIURL(t.Context()))

	statuses, maxResults, err = db.FindAndCount[git_model.CommitStatus](t.Context(), &git_model.CommitStatusOptions{
		ListOptions: db.ListOptions{Page: 2, PageSize: 50},
		RepoID:      repo1.ID,
		SHA:         sha1,
	})
	assert.NoError(t, err)
	assert.Equal(t, 5, int(maxResults))
	assert.Empty(t, statuses)
}

func TestGetLatestCommitStatusForRepoCommitIDs(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	repo1 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	sha1 := "1234123412341234123412341234123412341234" // the mocked commit ID in test fixtures

	statusMap, err := git_model.GetLatestCommitStatusForRepoCommitIDs(t.Context(), repo1.ID, []string{sha1})
	assert.NoError(t, err)

	// Only the latest status per context is kept: ci/awesomeness (index 4),
	// cov/awesomeness (index 3) and deploy/awesomeness (index 5).
	statuses := statusMap[sha1]
	assert.Len(t, statuses, 3)
	latest := make(map[string]int64, len(statuses))
	for _, s := range statuses {
		latest[s.Context] = s.Index
	}
	assert.Equal(t, map[string]int64{
		"ci/awesomeness":     4,
		"cov/awesomeness":    3,
		"deploy/awesomeness": 5,
	}, latest)
}

func TestGetLatestCommitStatusForRepoCommitIDsBatching(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	shaA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaB := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	// Prepare two distinct commits, each with several statuses sharing contexts
	// across multiple indexes, so the latest-per-context dedup is exercised.
	rows := []*git_model.CommitStatus{
		{RepoID: repo.ID, Index: 1, SHA: shaA, CreatorID: 2, Context: "lint", ContextHash: "hash-lint", State: commitstatus.CommitStatusPending},
		{RepoID: repo.ID, Index: 2, SHA: shaA, CreatorID: 2, Context: "lint", ContextHash: "hash-lint", State: commitstatus.CommitStatusSuccess},
		{RepoID: repo.ID, Index: 3, SHA: shaA, CreatorID: 2, Context: "build", ContextHash: "hash-build", State: commitstatus.CommitStatusFailure},
		{RepoID: repo.ID, Index: 1, SHA: shaB, CreatorID: 2, Context: "test", ContextHash: "hash-test", State: commitstatus.CommitStatusError},
	}
	for _, row := range rows {
		assert.NoError(t, db.Insert(t.Context(), row))
	}

	// Multiple SHAs in one call are grouped separately, keeping only the latest
	// status per context (shaA: lint index 2 + build index 3; shaB: test index 1).
	statusMap, err := git_model.GetLatestCommitStatusForRepoCommitIDs(t.Context(), repo.ID, []string{shaA, shaB})
	assert.NoError(t, err)
	assert.Len(t, statusMap[shaA], 2)
	assert.Len(t, statusMap[shaB], 1)

	// A SHA repeated on both sides of the batch boundary must still be deduped
	// to a single set of statuses (the seen map spans batches).
	commitIDs := make([]string, 0, 1002)
	commitIDs = append(commitIDs, shaA)
	for i := range 1000 {
		commitIDs = append(commitIDs, fmt.Sprintf("%040d", i))
	}
	commitIDs = append(commitIDs, shaA)
	statusMap, err = git_model.GetLatestCommitStatusForRepoCommitIDs(t.Context(), repo.ID, commitIDs)
	assert.NoError(t, err)
	assert.Len(t, statusMap[shaA], 2)

	// Empty input yields an empty, non-nil map.
	statusMap, err = git_model.GetLatestCommitStatusForRepoCommitIDs(t.Context(), repo.ID, nil)
	assert.NoError(t, err)
	assert.NotNil(t, statusMap)
	assert.Empty(t, statusMap)
}

func Test_CalcCommitStatus(t *testing.T) {
	kases := []struct {
		statuses []*git_model.CommitStatus
		expected *git_model.CommitStatus
	}{
		{
			statuses: []*git_model.CommitStatus{
				{
					State: commitstatus.CommitStatusPending,
				},
			},
			expected: &git_model.CommitStatus{
				State: commitstatus.CommitStatusPending,
			},
		},
		{
			statuses: []*git_model.CommitStatus{
				{
					State: commitstatus.CommitStatusSuccess,
				},
				{
					State: commitstatus.CommitStatusPending,
				},
			},
			expected: &git_model.CommitStatus{
				State: commitstatus.CommitStatusPending,
			},
		},
		{
			statuses: []*git_model.CommitStatus{
				{
					State: commitstatus.CommitStatusSuccess,
				},
				{
					State: commitstatus.CommitStatusPending,
				},
				{
					State: commitstatus.CommitStatusSuccess,
				},
			},
			expected: &git_model.CommitStatus{
				State: commitstatus.CommitStatusPending,
			},
		},
		{
			statuses: []*git_model.CommitStatus{
				{
					State: commitstatus.CommitStatusError,
				},
				{
					State: commitstatus.CommitStatusPending,
				},
				{
					State: commitstatus.CommitStatusSuccess,
				},
			},
			expected: &git_model.CommitStatus{
				State: commitstatus.CommitStatusFailure,
			},
		},
		{
			statuses: []*git_model.CommitStatus{
				{
					State: commitstatus.CommitStatusWarning,
				},
				{
					State: commitstatus.CommitStatusPending,
				},
				{
					State: commitstatus.CommitStatusSuccess,
				},
			},
			expected: &git_model.CommitStatus{
				State: commitstatus.CommitStatusFailure,
			},
		},
		{
			statuses: []*git_model.CommitStatus{
				{
					State: commitstatus.CommitStatusSuccess,
				},
				{
					State: commitstatus.CommitStatusSuccess,
				},
				{
					State: commitstatus.CommitStatusSuccess,
				},
			},
			expected: &git_model.CommitStatus{
				State: commitstatus.CommitStatusSuccess,
			},
		},
		{
			statuses: []*git_model.CommitStatus{
				{
					State: commitstatus.CommitStatusFailure,
				},
				{
					State: commitstatus.CommitStatusError,
				},
				{
					State: commitstatus.CommitStatusWarning,
				},
			},
			expected: &git_model.CommitStatus{
				State: commitstatus.CommitStatusFailure,
			},
		},
	}

	for _, kase := range kases {
		assert.Equal(t, kase.expected, git_model.CalcCommitStatus(kase.statuses), "statuses: %v", kase.statuses)
	}
}

func TestFindRepoRecentCommitStatusContexts(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	repo2 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 2})
	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	gitRepo, err := gitrepo.OpenRepository(t.Context(), repo2)
	assert.NoError(t, err)
	defer gitRepo.Close()

	commit, err := gitRepo.GetBranchCommit(repo2.DefaultBranch)
	assert.NoError(t, err)

	defer func() {
		_, err := db.DeleteByBean(t.Context(), &git_model.CommitStatus{
			RepoID:    repo2.ID,
			CreatorID: user2.ID,
			SHA:       commit.ID.String(),
		})
		assert.NoError(t, err)
	}()

	err = git_model.NewCommitStatus(t.Context(), git_model.NewCommitStatusOptions{
		Repo:    repo2,
		Creator: user2,
		SHA:     commit.ID,
		CommitStatus: &git_model.CommitStatus{
			State:     commitstatus.CommitStatusFailure,
			TargetURL: "https://example.com/tests/",
			Context:   "compliance/lint-backend",
		},
	})
	assert.NoError(t, err)

	err = git_model.NewCommitStatus(t.Context(), git_model.NewCommitStatusOptions{
		Repo:    repo2,
		Creator: user2,
		SHA:     commit.ID,
		CommitStatus: &git_model.CommitStatus{
			State:     commitstatus.CommitStatusSuccess,
			TargetURL: "https://example.com/tests/",
			Context:   "compliance/lint-backend",
		},
	})
	assert.NoError(t, err)

	contexts, err := git_model.FindRepoRecentCommitStatusContexts(t.Context(), repo2.ID, time.Hour)
	assert.NoError(t, err)
	if assert.Len(t, contexts, 1) {
		assert.Equal(t, "compliance/lint-backend", contexts[0])
	}
}

func TestCommitStatusesHideActionsURL(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 4})
	run := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: 791, RepoID: repo.ID})
	assert.NoError(t, run.LoadAttributes(t.Context()))

	statuses := []*git_model.CommitStatus{
		{
			RepoID:    repo.ID,
			TargetURL: fmt.Sprintf("%s/jobs/%d", run.Link(), run.ID),
		},
		{
			RepoID:    repo.ID,
			TargetURL: "https://mycicd.org/1",
		},
	}

	git_model.CommitStatusesHideActionsURL(t.Context(), statuses)
	assert.Empty(t, statuses[0].TargetURL)
	assert.Equal(t, "https://mycicd.org/1", statuses[1].TargetURL)
}

func TestGetCountLatestCommitStatus(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	repo1 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})

	sha1 := "1234123412341234123412341234123412341234" // the mocked commit ID in test fixtures

	commitStatuses, err := git_model.GetLatestCommitStatus(t.Context(), repo1.ID, sha1, db.ListOptions{
		Page:     1,
		PageSize: 2,
	})
	assert.NoError(t, err)
	assert.Len(t, commitStatuses, 2)
	assert.Equal(t, commitstatus.CommitStatusFailure, commitStatuses[0].State)
	assert.Equal(t, "ci/awesomeness", commitStatuses[0].Context)
	assert.Equal(t, commitstatus.CommitStatusError, commitStatuses[1].State)
	assert.Equal(t, "deploy/awesomeness", commitStatuses[1].Context)

	count, err := git_model.CountLatestCommitStatus(t.Context(), repo1.ID, sha1)
	assert.NoError(t, err)
	assert.EqualValues(t, 3, count)
}
