package metrics

import (
	"context"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-github/v45/github"
	"github.com/spendesk/github-actions-exporter/pkg/config"
)

// getJobFieldValue - returns a value for a given field from a GitHub workflow job
func getJobFieldValue(repo string, workflow string, event string, job *github.WorkflowJob, field string) string {
	switch field {
	case "repo":
		return repo
	case "workflow":
		return workflow
	case "job_name":
		return job.GetName()
	case "conclusion":
		return job.GetConclusion()
	case "event":
		return event
	case "run_id":
		return strconv.FormatInt(job.GetRunID(), 10)
	case "job_id":
		return strconv.FormatInt(job.GetID(), 10)
	}
	log.Printf("Tried to fetch invalid job field '%s'", field)
	return ""
}

func getRelevantJobFields(repo string, workflow string, event string, job *github.WorkflowJob) []string {
	relevantFields := strings.Split(config.WorkflowJobFields, ",")
	result := make([]string, len(relevantFields))
	for i, field := range relevantFields {
		result[i] = getJobFieldValue(repo, workflow, event, job, field)
	}
	return result
}

// getRelevantStepFields - job labels plus step_name, with "conclusion" holding the step's conclusion
func getRelevantStepFields(repo string, workflow string, event string, job *github.WorkflowJob, step *github.TaskStep) []string {
	result := getRelevantJobFields(repo, workflow, event, job)
	for i, field := range strings.Split(config.WorkflowJobFields, ",") {
		if field == "conclusion" {
			result[i] = step.GetConclusion()
		}
	}
	return append(result, step.GetName())
}

// inAllowList - "*" matches everything; an empty list matches nothing
func inAllowList(allowed []string, value string) bool {
	for _, a := range allowed {
		a = strings.TrimSpace(a)
		if a == "*" || (a != "" && a == value) {
			return true
		}
	}
	return false
}

// shouldExportSteps - step metrics multiply series by steps per job, so both the event and the
// workflow must be explicitly allowed; unset lists disable step metrics entirely
func shouldExportSteps(event string, workflow string) bool {
	return inAllowList(config.StepEvents.Value(), event) && inAllowList(config.StepWorkflows.Value(), workflow)
}

func emitStepDurations(repo string, workflow string, event string, job *github.WorkflowJob) {
	for _, step := range job.Steps {
		start := step.GetStartedAt()
		end := step.GetCompletedAt()
		if start.IsZero() || end.IsZero() {
			continue
		}
		fields := getRelevantStepFields(repo, workflow, event, job, step)
		workflowStepDurationGauge.WithLabelValues(fields...).Set(end.Time.Sub(start.Time).Seconds())
	}
}

func getJobsForRun(owner string, repo string, runID int64) []*github.WorkflowJob {
	opt := &github.ListWorkflowJobsOptions{
		ListOptions: github.ListOptions{PerPage: 100},
	}
	var jobs []*github.WorkflowJob
	for {
		resp, rr, err := client.Actions.ListWorkflowJobs(context.Background(), owner, repo, runID, opt)
		if rl_err, ok := err.(*github.RateLimitError); ok {
			log.Printf("ListWorkflowJobs ratelimited. Pausing until %s", rl_err.Rate.Reset.Time.String())
			time.Sleep(time.Until(rl_err.Rate.Reset.Time))
			continue
		} else if err != nil {
			log.Printf("ListWorkflowJobs error for run %d in repo %s/%s: %s", runID, owner, repo, err.Error())
			return jobs
		}
		jobs = append(jobs, resp.Jobs...)
		if rr.NextPage == 0 {
			break
		}
		opt.Page = rr.NextPage
	}
	return jobs
}

// getWorkflowJobsFromGithub - fetch jobs for each workflow run and emit metrics
func getWorkflowJobsFromGithub() {
	for {
		workflowJobStatusGauge.Reset()
		workflowJobDurationGauge.Reset()
		workflowStepDurationGauge.Reset()
		for _, repo := range repositories {
			r := strings.Split(repo, "/")
			runs := getRecentWorkflowRuns(r[0], r[1])

			// Create a map of run IDs to their events
			runEventMap := make(map[int64]string)
			for _, run := range runs {
				runEventMap[run.GetID()] = run.GetEvent()
			}

			for _, run := range runs {
				event := runEventMap[run.GetID()]
				workflowName := getFieldValue(repo, *run, "workflow")
				jobs := getJobsForRun(r[0], r[1], run.GetID())
				exportSteps := shouldExportSteps(event, workflowName)

				for _, job := range jobs {
					fields := getRelevantJobFields(repo, workflowName, event, job)

					var status float64 = 0
					switch job.GetConclusion() {
					case "success":
						status = 1
					case "skipped":
						status = 2
					case "in_progress":
						status = 3
					case "queued":
						status = 4
					}
					workflowJobStatusGauge.WithLabelValues(fields...).Set(status)

					start := job.GetStartedAt()
					end := job.GetCompletedAt()

					if !start.IsZero() && !end.IsZero() {
						duration := end.Time.Sub(start.Time).Seconds()
						workflowJobDurationGauge.WithLabelValues(fields...).Set(duration)
					} else {
						log.Printf("Skipping duration metric for job %s in run %d (start or end time missing)", job.GetName(), job.GetRunID())
					}

					if exportSteps {
						emitStepDurations(repo, workflowName, event, job)
					}
				}
			}
		}
		time.Sleep(time.Duration(config.Github.Refresh) * time.Second)
	}
}
