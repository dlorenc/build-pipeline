/*
Copyright 2018 The Knative Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package resources

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	v1alpha1 "github.com/knative/build-pipeline/pkg/apis/pipeline/v1alpha1"
	"github.com/knative/build-pipeline/pkg/logging"
	buildv1alpha1 "github.com/knative/build/pkg/apis/build/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

var simpleBuild = &buildv1alpha1.Build{
	Spec: buildv1alpha1.BuildSpec{
		Steps: []corev1.Container{
			{
				Name:  "myname",
				Image: "myimage",
			},
		},
	},
}

var gitResource = v1alpha1.TaskResource{
	Name: "myresource",
	Type: "git",
}
var imageResource = v1alpha1.TaskResource{
	Name: "myotherresource",
	Type: "image",
}

var xmlTestResult = v1alpha1.TestResult{
	Name:   "unit",
	Format: "junitxml",
	Path:   "/workspace/foo.xml",
}
var plainTestResult = v1alpha1.TestResult{
	Name:   "otherunit",
	Format: "plaintext",
	Path:   "/workspace/foo.txt",
}

func TestAddOutputResources(t *testing.T) {
	type args struct {
		build   *buildv1alpha1.Build
		outputs *v1alpha1.Outputs
		taskRun *v1alpha1.TaskRun
	}
	tests := []struct {
		name string
		args args
		want []string
	}{
		{
			name: "no outputs",
			args: args{
				build:   simpleBuild,
				outputs: &v1alpha1.Outputs{},
			},
			want: []string{},
		},
		{
			name: "single resource output",
			args: args{
				build: simpleBuild,
				outputs: &v1alpha1.Outputs{
					Resources: []v1alpha1.TaskResource{gitResource},
				},
			},
			want: []string{"--resource=myresource,git"},
		},
		{
			name: "multiple resource output",
			args: args{
				build: simpleBuild,
				outputs: &v1alpha1.Outputs{
					Resources: []v1alpha1.TaskResource{gitResource, imageResource},
				},
			},
			want: []string{"--resource=myresource,git", "--resource=myotherresource,image"},
		},
		{
			name: "single result output",
			args: args{
				build: simpleBuild,
				outputs: &v1alpha1.Outputs{
					Results: []v1alpha1.TestResult{xmlTestResult},
				},
			},
			want: []string{"--result=unit,junitxml,/workspace/foo.xml"},
		},
		{
			name: "multiple result outputs",
			args: args{
				build: simpleBuild,
				outputs: &v1alpha1.Outputs{
					Results: []v1alpha1.TestResult{xmlTestResult, plainTestResult},
				},
			},
			want: []string{"--result=unit,junitxml,/workspace/foo.xml", "--result=otherunit,plaintext,/workspace/foo.txt"},
		},
		{
			name: "multiple results and resources",
			args: args{
				build: simpleBuild,
				outputs: &v1alpha1.Outputs{
					Resources: []v1alpha1.TaskResource{gitResource, imageResource},
					Results:   []v1alpha1.TestResult{xmlTestResult, plainTestResult},
				},
			},
			want: []string{"--result=unit,junitxml,/workspace/foo.xml", "--result=otherunit,plaintext,/workspace/foo.txt", "--resource=myresource,git", "--resource=myotherresource,image"},
		},
	}
	logger, _ = logging.NewLogger("", "")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			task := &v1alpha1.Task{
				Spec: v1alpha1.TaskSpec{
					Outputs: tt.args.outputs,
				},
			}
			wantedBuild := tt.args.build.DeepCopy()

			got := AddOutputResources(tt.args.build, task, tt.args.taskRun, logger)

			step := corev1.Container{
				Name:  uploadStepName,
				Image: uploadStepImage,
				Args:  tt.want,
			}

			// We want a single step appended to the existing build steps with the described content.
			wantedBuild.Spec.Steps = append(wantedBuild.Spec.Steps, step)
			if d := cmp.Diff(got, wantedBuild); d != "" {
				t.Errorf("Diff:\n%s", d)
			}

		})
	}
}
