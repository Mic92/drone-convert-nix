package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"io"
	"time"

	"github.com/drone/drone-go/drone"
	"github.com/drone/drone-go/plugin/converter"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

type transport struct {
	underlyingTransport http.RoundTripper
	token               string
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Add("Authorization", "Bearer "+t.token)
	return t.underlyingTransport.RoundTrip(req)
}

// New returns a new conversion plugin.
func New(host string, token string) converter.Plugin {
	client := http.Client{Transport: &transport{
		underlyingTransport: http.DefaultTransport,
		token:               token,
	}}
	// create the drone client with authenticator
	droneClient := drone.NewClient(host, &client)

	return &plugin{
		droneClient,
	}
}

type (
	plugin struct {
		drone drone.Client
	}
)

type resource map[interface{}]interface{}

func unmarshal(data []byte) ([]resource, error) {
	buf := bytes.NewBuffer(data)
	d := yaml.NewDecoder(buf)
	res := make([]resource, 0)
	for {
		var r resource
		if err := d.Decode(&r); err != nil {
			if err != io.EOF {
				return nil, err
			}
			return res, nil
		}
		res = append(res, r)
	}
}

func isPipeline(r resource) bool {
	kind, ok := r["kind"]
	if !ok {
		return false
	}

	k, ok := kind.(string)
	return ok && k == "pipeline"
}

func isJobset(r resource) bool {
	maybeJobset, ok := r["nix-jobset"]
	if !ok {
		return false
	}

	jobset, ok := maybeJobset.(bool)
	return ok && jobset
}

func isNixBuild(r resource) bool {
	maybeBuild, ok := r["nix-build"]
	if !ok {
		return false
	}

	build, ok := maybeBuild.(bool)
	return ok && build
}

func getNixStages(resources []resource) ([]resource, []resource, []resource) {
	var (
		jobsets []resource
		builds  []resource
		others  []resource
	)

	for _, resource := range resources {
		if isPipeline(resource) {
			if isJobset(resource) {
				delete(resource, "nix-jobset")
				jobsets = append(jobsets, resource)
			} else if isNixBuild(resource) {
				delete(resource, "nix-build")
				builds = append(builds, resource)
			}
		} else {
			others = append(others, resource)
		}
	}
	return jobsets, builds, others
}

func (p *plugin) createEvalBuild(req *converter.Request, jobsetStages []resource) (*drone.Build, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	for _, jobset := range jobsetStages {
		err := enc.Encode(jobset)
		if err != nil {
			return nil, fmt.Errorf("cannot convert jobset `%v` to yaml: %v", jobset, err)
		}
	}
	err := enc.Close()
	if err != nil {
		return nil, fmt.Errorf("cannot convert jobsets to yaml: %v", err)
	}

	params := map[string]string{
		"nix_eval_jobset": buf.String(),
	}
	build, err := p.drone.BuildCreate(req.Repo.Namespace, req.Repo.Name, req.Build.Ref, req.Repo.Branch, params)
	if err != nil {
		return nil, fmt.Errorf("cannot create build: %v", err)
	}

	return build, nil
}

type Job struct {
	DrvPath string   `json:"drvPath"`
	Builds  []string `json:"builds"`
}

func parseEvalLogs(lines []*drone.Line) (map[string]Job, error) {
	evalOutput := make([]string, 0, len(lines))
	inEvalOutput := false
	for _, line := range lines {
		if !inEvalOutput {
			if line.Message == "<hydra-eval-jobs>" {
				inEvalOutput = true
			}
		} else if line.Message == "</hydra-eval-jobs>" {
			break
		} else {
			evalOutput = append(evalOutput, line.Message)
		}
	}

	var jobs map[string]Job
	err := json.Unmarshal([]byte(strings.Join(evalOutput, "\n")), &jobs)
	if err != nil {
		return nil, fmt.Errorf("failed to decode hydra-eval-jobs output: %v", err)
	}
	return jobs, nil
}

func (p *plugin) evalJobsets(req *converter.Request, jobsetStages []resource) (map[string]Job, error) {
	build, err := p.createEvalBuild(req, jobsetStages)
	if err != nil {
		return nil, fmt.Errorf("cannot start nix evaluation build: %v", err)
	}

	for {
		b, err := p.drone.Build(req.Repo.Namespace, req.Repo.Name, int(build.Number))
		if err != nil {
			return nil, fmt.Errorf("cannot get build status: %v", err)
		}
		if b.Status == "pending" || b.Status == "running" {
			time.Sleep(500 * time.Millisecond)
			continue
		} else if b.Status != "success" {
			// TODO better error message
			return nil, fmt.Errorf("evaluation failed: %v", b.ID)
		} else {
			break
		}
	}

	allJobs := make(map[string]Job)
	for _, stage := range build.Stages {
		for _, step := range stage.Steps {
			logs, err := p.drone.Logs(req.Repo.Namespace, req.Repo.Name, int(build.Number), stage.Number, step.Number)
			if err != nil {
				return nil, fmt.Errorf("cannot get logs for step %s/%s/%d/%d/%d: %v", req.Repo.Namespace, req.Repo.Name, build.Number, stage.Number, step.Number, err)
			}
			jobs, err := parseEvalLogs(logs)
			if err != nil {
				return nil, fmt.Errorf("failed to parse evaluation logs: %v", err)
			}
			for k, v := range jobs {
				allJobs[k] = v
			}
		}
	}
	return allJobs, nil
}

func populateNixStage(stage resource, jobName string, job *Job) {
	nameField, ok := stage["Name"]
	if ok {
		name, ok := nameField.(*string)
		if ok {
			stage["Name"] = fmt.Sprintf("%s (%s)", *name, jobName)
		} else {
			stage["Name"] = jobName
		}
	} else {
		stage["Name"] = jobName
	}

	envField, ok := stage["Environment"]
	if ok {
		env, ok := envField.(*map[string]string)
		if ok {
			(*env)["drvPath"] = job.DrvPath
		} else {
			stage["Environment"] = map[string]string{
				"drvPath": job.DrvPath,
			}
		}

	} else {
		stage["Environment"] = map[string]string{
			"drvPath": job.DrvPath,
		}
	}
}

func renderConfig(nixStages []resource, others []resource, jobs map[string]Job) (string, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	for other := range others {
		err := enc.Encode(other)
		if err != nil {
			return "", fmt.Errorf("cannot convert pipeline stage yaml: %v", err)
		}
	}
	for jobName, job := range jobs {
		if len(job.Builds) == 0 {
			continue
		}
		for _, n := range nixStages {
			populateNixStage(n, jobName, &job)
			err := enc.Encode(n)
			if err != nil {
				return "", fmt.Errorf("cannot convert jobset `%s` to yaml: %v", jobName, err)
			}
		}
	}
	err := enc.Close()

	if err != nil {
		return "", fmt.Errorf("cannot convert jobsets to yaml: %v", err)
	}

	return buf.String(), nil

}

func (p *plugin) Convert(ctx context.Context, req *converter.Request) (*drone.Config, error) {
	// set some default fields for logs
	requestLogger := logrus.WithFields(logrus.Fields{
		"build_after":    req.Build.After,
		"build_before":   req.Build.Before,
		"repo_namespace": req.Repo.Namespace,
		"repo_name":      req.Repo.Name,
	})

	// initial log message with extra fields
	requestLogger.WithFields(logrus.Fields{
		"build_action":  req.Build.Action,
		"build_event":   req.Build.Event,
		"build_source":  req.Build.Source,
		"build_ref":     req.Build.Ref,
		"build_target":  req.Build.Target,
		"build_trigger": req.Build.Trigger,
	}).Infoln("initiated")

	requestLogger.Infoln("process " + req.Repo.Config)

	if req.Build.Event == "custom" {
		nixEvalJobset, ok := req.Build.Params["nix_eval_jobset"]
		if ok {
			return &drone.Config{
				Data: nixEvalJobset,
			}, nil
		}
	}

	// get the configuration file from the request.
	config := req.Config.Data

	resources, err := unmarshal([]byte(config))
	if err != nil {
		return nil, fmt.Errorf("cannot decode config: %v", err)
	}

	jobsetStages, buildStages, others := getNixStages(resources)

	if len(jobsetStages) == 0 {
		return nil, fmt.Errorf("no pipeline found with nix-jobset flag set")
	}

	if len(buildStages) == 0 {
		return nil, fmt.Errorf("no pipeline found with nix-build flag set")
	}

	jobs, err := p.evalJobsets(req, jobsetStages)
	if err != nil {
		return nil, fmt.Errorf("cannot evaluate jobsets: %v", err)
	}

	config, err = renderConfig(buildStages, others, jobs)

	if err != nil {
		return nil, fmt.Errorf("cannot generate pipeline configuration: %v", err)
	}

	// returns the modified configuration file.
	return &drone.Config{
		Data: config,
	}, nil
}
