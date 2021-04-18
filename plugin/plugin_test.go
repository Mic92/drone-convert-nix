// Copyright 2019 the Drone Authors. All rights reserved.
// Use of this source code is governed by the Blue Oak Model License
// that can be found in the LICENSE file.

package plugin

import (
	"context"
	"testing"
	"strings"
	"fmt"
	"runtime"
	"path/filepath"
	"encoding/json"
	"github.com/drone/drone-go/drone"
	"github.com/drone/drone-go/plugin/converter"
	"gopkg.in/h2non/gock.v1"
)

// ok fails the test if an err is not nil.
func ok(tb testing.TB, err error) {
	if err != nil {
		_, file, line, _ := runtime.Caller(1)
		fmt.Printf("\033[31m%s:%d: unexpected error: %s\033[39m\n\n", filepath.Base(file), line, err.Error())
		tb.FailNow()
	}
}


func TestPlugin(t *testing.T) {
	defer gock.Off() // Flush pending mocks after test execution
	// for debugging
	//gock.Observe(gock.DumpRequest)

	pipeline := `---
kind: pipeline
type: exec
nix-jobset: true
name: Eval jobset
commands:
  - rm -rf $BUILDDIR/gcroots.tmp && mkdir -p $BUILDDIR/gcroots.tmp
  - echo <hydra-eval-jobs>
  - nix run --no-write-lock-file --override-input nixpkgs github:Mic92/nixpkgs https://github.com/Mic92/hydra-eval-jobs github:Mic92/drone-nix-scheduler#hydra-eval-jobs -- --workers 4 --gc-roots-dir $PWD/gcroots --flake .# > eval.json
  - echo </hydra-eval-jobs>
  - rm -rf $BUILDDIR/gcroots && mv $BUILDDIR/gcroots.tmp $BUILDDIR/gcroots
environment:
  BUILDDIR: /var/lib/drone/nix-build
---
kind: pipeline
type: exec
name: Build job
nix-build: true

platform:
  os: linux
  arch: amd64

steps:
- name: build
  commands:
  - nix build -L $derivation
`
	build := drone.Build {
   	Number: 42,
		Status: "success",
		Stages: []*drone.Stage{
			&drone.Stage{
				Number: 43,
				Steps: []*drone.Step{
					&drone.Step{
						Number: 44,
					},
				},
			},
		},
	}
	buildJson, err := json.Marshal(build)
	ok(t, err)
	gock.New("http://drone-server").
		Post("/api/repos/Mic92/drone-convert-nix/builds").
		Reply(200).
		JSON(buildJson)
	gock.New("http://drone-server").
		Get("/api/repos/Mic92/drone-convert-nix/builds/42").
		Reply(200).
		JSON(buildJson)

	eval := map[string]Job{
		"job": Job {
		  DrvPath: "/nix/store/foo.drv",
			Builds: []string{
				"/nix/store/some-dep.drv",
			},
		},
	}
	evalJson, err := json.Marshal(eval)
	ok(t, err)

	logs := []drone.Line {
		drone.Line{ Number: 0, Message: "<hydra-eval-jobs>", },
		drone.Line{ Number: 1, Message: string(evalJson), },
		drone.Line{ Number: 2, Message: "</hydra-eval-jobs>", },
	}
	buildLogs, err := json.Marshal(logs)
	gock.New("http://drone-server").
		Get("/api/repos/Mic92/drone-convert-nix/builds/42/logs/43/44").
		Reply(200).
		JSON(buildLogs)

	req := &converter.Request{
		Build: drone.Build{
			Before: "",
			After:  "6ee3cf41d995a79857e0db41c47bf619e6546571",
		},
		Config: drone.Config{
			Data: pipeline,
		},
		Repo: drone.Repo{
			Namespace: "Mic92",
			Name:      "drone-convert-nix",
			Slug:      "mic92/drone-convert-nix",
			Config:    ".drone.yml",
		},
	}
	plugin := New("http://drone-server", "api-token")
	config, err := plugin.Convert(context.Background(), req)
	ok(t, err)
	if !strings.Contains(config.Data, eval["job"].DrvPath) {
		t.Fatalf("returned yaml template does not contain: %s: %s", eval["job"].DrvPath, config.Data)
	}
}
