package metrics

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/spendesk/github-actions-exporter/pkg/config"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/die-net/lrucache"
	"github.com/google/go-github/v45/github"
	"github.com/gregjones/httpcache"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/oauth2"
)

var (
	client                   *github.Client
	err                      error
	workflowRunStatusGauge   *prometheus.GaugeVec
	workflowRunDurationGauge *prometheus.GaugeVec
	workflowJobStatusGauge   *prometheus.GaugeVec
	workflowJobDurationGauge *prometheus.GaugeVec
	queuedJobsGauge          *prometheus.GaugeVec
	queuedJobsOldestGauge    *prometheus.GaugeVec
)

// InitMetrics - register metrics in prometheus lib and start func for monitor
func InitMetrics() {
	workflowRunStatusGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "github_workflow_run_status",
			Help: "Workflow run status of all workflow runs created in the last 12hr",
		},
		strings.Split(config.WorkflowFields, ","),
	)
	workflowRunDurationGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "github_workflow_run_duration_ms",
			Help: "Workflow run duration (in milliseconds) of all workflow runs created in the last 12hr",
		},
		strings.Split(config.WorkflowFields, ","),
	)
	workflowJobStatusGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "github_workflow_job_status",
			Help: "Status of each job inside GitHub workflow run",
		},
		strings.Split(config.WorkflowJobFields, ","),
	)
	workflowJobDurationGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "github_workflow_job_duration_seconds",
			Help: "Duration of each GitHub workflow job in seconds",
		},
		strings.Split(config.WorkflowJobFields, ","),
	)
	queuedJobsGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "github_workflow_jobs_queued",
			Help: "Number of workflow jobs waiting for a runner, by repo and runs-on labels",
		},
		[]string{"repo", "labels"},
	)
	queuedJobsOldestGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "github_workflow_jobs_queued_oldest_seconds",
			Help: "Age of the oldest workflow job waiting for a runner, by repo and runs-on labels",
		},
		[]string{"repo", "labels"},
	)
	prometheus.MustRegister(runnersGauge)
	prometheus.MustRegister(runnersOrganizationGauge)
	prometheus.MustRegister(workflowRunStatusGauge)
	prometheus.MustRegister(workflowRunDurationGauge)
	prometheus.MustRegister(workflowJobStatusGauge)
	prometheus.MustRegister(workflowJobDurationGauge)
	prometheus.MustRegister(queuedJobsGauge)
	prometheus.MustRegister(queuedJobsOldestGauge)
	prometheus.MustRegister(workflowBillGauge)
	prometheus.MustRegister(runnersEnterpriseGauge)

	client, err = NewClient()
	if err != nil {
		log.Fatalln("Error: Client creation failed." + err.Error())
	}

	go periodicGithubFetcher()

	for {
		if workflows != nil {
			break
		}
	}

	go getBillableFromGithub()
	go getRunnersFromGithub()
	go getRunnersOrganizationFromGithub()
	go getWorkflowRunsFromGithub()
	go getWorkflowJobsFromGithub()
	go getQueuedJobsFromGithub()
	go getRunnersEnterpriseFromGithub()
}

// NewClient creates a Github Client
func NewClient() (*github.Client, error) {
	var (
		httpClient      *http.Client
		client          *github.Client
		cachedTransport *httpcache.Transport
	)

	cache := lrucache.New(config.Github.CacheSizeBytes, 0)
	cachedTransport = httpcache.NewTransport(cache)

	if len(config.Github.Token) > 0 {
		log.Printf("authenticating with Github Token")
		ctx := context.Background()
		ctx = context.WithValue(ctx, "HTTPClient", cachedTransport.Client())
		httpClient = oauth2.NewClient(ctx, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: config.Github.Token}))
	} else {
		log.Printf("authenticating with Github App")
		transport, err := ghinstallation.NewKeyFromFile(cachedTransport, config.Github.AppID, config.Github.AppInstallationID, config.Github.AppPrivateKey)
		if err != nil {
			return nil, fmt.Errorf("authentication failed: %v", err)
		}
		if config.Github.APIURL != "api.github.com" {
			githubAPIURL, err := getEnterpriseApiUrl(config.Github.APIURL)
			if err != nil {
				return nil, fmt.Errorf("enterprise url incorrect: %v", err)
			}
			transport.BaseURL = githubAPIURL
		}
		httpClient = &http.Client{Transport: transport}
	}

	if config.Github.APIURL != "api.github.com" {
		var err error
		client, err = github.NewEnterpriseClient(config.Github.APIURL, config.Github.APIURL, httpClient)
		if err != nil {
			return nil, fmt.Errorf("enterprise client creation failed: %v", err)
		}
	} else {
		client = github.NewClient(httpClient)
	}

	return client, nil
}

func getEnterpriseApiUrl(baseURL string) (string, error) {
	baseEndpoint, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	if !strings.HasSuffix(baseEndpoint.Path, "/") {
		baseEndpoint.Path += "/"
	}
	if !strings.HasSuffix(baseEndpoint.Path, "/api/v3/") &&
		!strings.HasPrefix(baseEndpoint.Host, "api.") &&
		!strings.Contains(baseEndpoint.Host, ".api.") {
		baseEndpoint.Path += "api/v3/"
	}

	// Trim trailing slash, otherwise there's double slash added to token endpoint
	return fmt.Sprintf("%s://%s%s", baseEndpoint.Scheme, baseEndpoint.Host, strings.TrimSuffix(baseEndpoint.Path, "/")), nil
}
