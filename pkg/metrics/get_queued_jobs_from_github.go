package metrics

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/google/go-github/v45/github"
	"github.com/spendesk/github-actions-exporter/pkg/config"
)

type queuedKey struct {
	repo   string
	labels string
}

func getActiveWorkflowRuns(owner string, repo string) []*github.WorkflowRun {
	var runs []*github.WorkflowRun
	for _, status := range []string{"queued", "in_progress"} {
		opt := &github.ListWorkflowRunsOptions{
			Status:      status,
			ListOptions: github.ListOptions{PerPage: 100},
		}
		for {
			resp, rr, err := client.Actions.ListRepositoryWorkflowRuns(context.Background(), owner, repo, opt)
			if rl_err, ok := err.(*github.RateLimitError); ok {
				log.Printf("ListRepositoryWorkflowRuns ratelimited. Pausing until %s", rl_err.Rate.Reset.Time.String())
				time.Sleep(time.Until(rl_err.Rate.Reset.Time))
				continue
			} else if err != nil {
				log.Printf("ListRepositoryWorkflowRuns error for repo %s/%s (status %s): %s", owner, repo, status, err.Error())
				break
			}
			runs = append(runs, resp.WorkflowRuns...)
			if rr.NextPage == 0 {
				break
			}
			opt.Page = rr.NextPage
		}
	}
	return runs
}

// countQueuedJobs - queued jobs and oldest queue age per runs-on labels; jobs without labels are ignored
func countQueuedJobs(repo string, jobs []*github.WorkflowJob, now time.Time, count map[queuedKey]float64, oldest map[queuedKey]float64) {
	for _, job := range jobs {
		if len(job.Labels) == 0 {
			continue
		}
		key := queuedKey{repo: repo, labels: strings.Join(job.Labels, ",")}
		if _, seen := count[key]; !seen {
			count[key] = 0
		}
		if job.GetStatus() != "queued" {
			continue
		}
		count[key]++
		if started := job.GetStartedAt(); !started.IsZero() {
			if age := now.Sub(started.Time).Seconds(); age > oldest[key] {
				oldest[key] = age
			}
		}
	}
}

// getQueuedJobsFromGithub - count jobs waiting for a runner, by repo and runs-on labels.
// Only queued and in-progress runs are inspected, so one cycle is a handful of API calls.
func getQueuedJobsFromGithub() {
	for {
		count := make(map[queuedKey]float64)
		oldest := make(map[queuedKey]float64)
		now := time.Now()

		for _, repo := range repositories {
			r := strings.Split(repo, "/")
			for _, run := range getActiveWorkflowRuns(r[0], r[1]) {
				countQueuedJobs(repo, getJobsForRun(r[0], r[1], run.GetID()), now, count, oldest)
			}
		}

		queuedJobsGauge.Reset()
		queuedJobsOldestGauge.Reset()
		for key, n := range count {
			queuedJobsGauge.WithLabelValues(key.repo, key.labels).Set(n)
			queuedJobsOldestGauge.WithLabelValues(key.repo, key.labels).Set(oldest[key])
		}

		time.Sleep(time.Duration(config.Github.Refresh) * time.Second)
	}
}
