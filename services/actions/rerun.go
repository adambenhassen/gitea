// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	actions_model "code.gitea.io/gitea/models/actions"
	"code.gitea.io/gitea/modules/container"
)

// GetAllRerunJobs get all jobs that need to be rerun when job should be rerun
func GetAllRerunJobs(job *actions_model.ActionRunJob, allJobs []*actions_model.ActionRunJob) []*actions_model.ActionRunJob {
	rerunJobs := []*actions_model.ActionRunJob{job}
	rerunJobsIDSet := make(container.Set[string])
	rerunJobsIDSet.Add(job.JobID)

	for {
		found := false
		for _, j := range allJobs {
			if rerunJobsIDSet.Contains(j.JobID) {
				continue
			}
			for _, need := range j.Needs {
				if rerunJobsIDSet.Contains(need) {
					found = true
					rerunJobs = append(rerunJobs, j)
					rerunJobsIDSet.Add(j.JobID)
					break
				}
			}
		}
		if !found {
			break
		}
	}

	return rerunJobs
}

// GetAllRerunJobsFromFailed gets all failed jobs and jobs that depend on them
func GetAllRerunJobsFromFailed(allJobs []*actions_model.ActionRunJob) []*actions_model.ActionRunJob {
	rerunJobsIDSet := make(container.Set[string])
	var rerunJobs []*actions_model.ActionRunJob

	// Collect all failed jobs
	for _, job := range allJobs {
		if job.Status == actions_model.StatusFailure {
			rerunJobs = append(rerunJobs, job)
			rerunJobsIDSet.Add(job.JobID)
		}
	}

	// Iteratively add jobs that depend on failed/rerun jobs
	for {
		found := false
		for _, j := range allJobs {
			if rerunJobsIDSet.Contains(j.JobID) {
				continue
			}
			for _, need := range j.Needs {
				if rerunJobsIDSet.Contains(need) {
					found = true
					rerunJobs = append(rerunJobs, j)
					rerunJobsIDSet.Add(j.JobID)
					break
				}
			}
		}
		if !found {
			break
		}
	}

	return rerunJobs
}
