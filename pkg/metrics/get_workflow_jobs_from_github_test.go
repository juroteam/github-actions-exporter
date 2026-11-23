package metrics

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v45/github"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/urfave/cli/v2"

	"github.com/spendesk/github-actions-exporter/pkg/config"
)

func withStepConfig(t *testing.T, jobFields string, events []string, workflows []string) {
	t.Helper()
	prevFields, prevEvents, prevWorkflows := config.WorkflowJobFields, config.StepEvents, config.StepWorkflows
	t.Cleanup(func() {
		config.WorkflowJobFields, config.StepEvents, config.StepWorkflows = prevFields, prevEvents, prevWorkflows
	})
	config.WorkflowJobFields = jobFields
	config.StepEvents = *cli.NewStringSlice(events...)
	config.StepWorkflows = *cli.NewStringSlice(workflows...)
}

func TestInAllowList(t *testing.T) {
	cases := []struct {
		name    string
		allowed []string
		value   string
		want    bool
	}{
		{"nil matches nothing", nil, "push", false},
		{"blank entries match nothing", []string{"", " "}, "push", false},
		{"blank entry does not match empty value", []string{""}, "", false},
		{"star matches all", []string{"*"}, "push", true},
		{"star alongside others", []string{"merge_group", " * "}, "push", true},
		{"listed value", []string{"merge_group", "push"}, "push", true},
		{"whitespace trimmed", []string{" merge_group "}, "merge_group", true},
		{"unlisted value", []string{"merge_group"}, "pull_request", false},
		{"no glob semantics beyond a lone star", []string{"merge_*"}, "merge_group", false},
	}
	for _, c := range cases {
		if got := inAllowList(c.allowed, c.value); got != c.want {
			t.Errorf("%s: inAllowList(%v, %q) = %v, want %v", c.name, c.allowed, c.value, got, c.want)
		}
	}
}

func TestShouldExportSteps(t *testing.T) {
	withStepConfig(t, "repo,workflow,job_name,conclusion,event,run_id", []string{"merge_group"}, []string{"PR Checks", "Storybook"})

	if !shouldExportSteps("merge_group", "PR Checks") {
		t.Error("merge_group / PR Checks should export steps")
	}
	if shouldExportSteps("pull_request", "PR Checks") {
		t.Error("pull_request should be filtered out by EXPORT_STEP_EVENTS")
	}
	if shouldExportSteps("merge_group", "Secret scan") {
		t.Error("Secret scan should be filtered out by EXPORT_STEP_WORKFLOWS")
	}

	withStepConfig(t, "repo,workflow,job_name,conclusion,event,run_id", nil, nil)
	if shouldExportSteps("merge_group", "PR Checks") {
		t.Error("with neither env set, step metrics must be off")
	}

	withStepConfig(t, "repo,workflow,job_name,conclusion,event,run_id", []string{"*"}, nil)
	if shouldExportSteps("merge_group", "PR Checks") {
		t.Error("EXPORT_STEP_WORKFLOWS unset must keep step metrics off even when events is *")
	}

	withStepConfig(t, "repo,workflow,job_name,conclusion,event,run_id", []string{"merge_group"}, []string{"*"})
	if !shouldExportSteps("merge_group", "Secret scan") {
		t.Error("workflows=* should allow any workflow for a listed event")
	}
	if shouldExportSteps("push", "Secret scan") {
		t.Error("workflows=* must not widen the event filter")
	}
}

func TestEmitStepDurations(t *testing.T) {
	jobFields := "repo,workflow,job_name,conclusion,event,run_id"
	withStepConfig(t, jobFields, []string{"*"}, []string{"*"})

	prevGauge := workflowStepDurationGauge
	t.Cleanup(func() { workflowStepDurationGauge = prevGauge })
	workflowStepDurationGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Name: "test_step_duration_seconds"},
		append(strings.Split(jobFields, ","), "step_name"),
	)

	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	str := func(s string) *string { return &s }
	i64 := func(i int64) *int64 { return &i }
	ts := func(offset time.Duration) *github.Timestamp { return &github.Timestamp{Time: now.Add(offset)} }

	job := &github.WorkflowJob{
		RunID:      i64(42),
		Name:       str("Run Playwright tests (1/7)"),
		Conclusion: str("failure"),
		Steps: []*github.TaskStep{
			{Name: str("Install deps"), Conclusion: str("success"), StartedAt: ts(0), CompletedAt: ts(95 * time.Second)},
			{Name: str("Run tests"), Conclusion: str("failure"), StartedAt: ts(95 * time.Second), CompletedAt: ts(10 * time.Minute)},
			{Name: str("Upload report"), Conclusion: str("skipped")},
		},
	}

	gotFields := getRelevantStepFields("juroteam/juro-nx", "PR Checks", "merge_group", job, job.Steps[0])
	wantFields := []string{"juroteam/juro-nx", "PR Checks", "Run Playwright tests (1/7)", "success", "merge_group", "42", "Install deps"}
	if !reflect.DeepEqual(gotFields, wantFields) {
		t.Fatalf("step fields = %v, want %v (conclusion must be the step's, not the job's)", gotFields, wantFields)
	}

	emitStepDurations("juroteam/juro-nx", "PR Checks", "merge_group", job)

	if n := testutil.CollectAndCount(workflowStepDurationGauge); n != 2 {
		t.Fatalf("emitted %d series, want 2 (steps without timestamps are skipped)", n)
	}
	install := workflowStepDurationGauge.WithLabelValues(wantFields...)
	if got := testutil.ToFloat64(install); got != 95 {
		t.Errorf("Install deps duration = %v, want 95", got)
	}
	run := workflowStepDurationGauge.WithLabelValues("juroteam/juro-nx", "PR Checks", "Run Playwright tests (1/7)", "failure", "merge_group", "42", "Run tests")
	if got := testutil.ToFloat64(run); got != 505 {
		t.Errorf("Run tests duration = %v, want 505", got)
	}
}
