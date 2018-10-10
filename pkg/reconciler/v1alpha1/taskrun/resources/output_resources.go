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
	"fmt"

	v1alpha1 "github.com/knative/build-pipeline/pkg/apis/pipeline/v1alpha1"
	buildv1alpha1 "github.com/knative/build/pkg/apis/build/v1alpha1"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
)

const (
	uploadStepName  = "build-pipeline.knative.dev/uploader"
	uploadStepImage = "gcr.io/something/else"
)

// AddOutputResources will update the input build with the output resources and results from the task.
func AddOutputResources(build *buildv1alpha1.Build,
	task *v1alpha1.Task,
	taskRun *v1alpha1.TaskRun,
	logger *zap.SugaredLogger,
) *buildv1alpha1.Build {

	// Build up flags to pass to the upload container.
	flags := []string{}

	// Result flags are formatted as --result=name,format,path
	for _, output := range task.Spec.Outputs.Results {
		flag := fmt.Sprintf("--result=%s,%s,%s", output.Name, output.Format, output.Path)
		flags = append(flags, flag)
	}

	// Resource flags are formatted as --result=name,type
	for _, output := range task.Spec.Outputs.Resources {
		flag := fmt.Sprintf("--resource=%s,%s", output.Name, output.Type)
		flags = append(flags, flag)
	}

	upload := corev1.Container{
		Args:  flags,
		Name:  uploadStepName,
		Image: uploadStepImage,
	}
	build.Spec.Steps = append(build.Spec.Steps, upload)
	return build
}
