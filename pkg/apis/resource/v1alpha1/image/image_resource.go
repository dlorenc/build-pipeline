/*
Copyright 2019-2020 The Tekton Authors

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

package image

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn/k8schain"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/google/go-containerregistry/pkg/v1/types"
	pipelinev1beta1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"

	pipelinev1alpha1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1alpha1"
	resourcev1alpha1 "github.com/tektoncd/pipeline/pkg/apis/resource/v1alpha1"
	"github.com/tektoncd/pipeline/pkg/reconciler/signing"
	"github.com/tektoncd/pipeline/pkg/version"
	"go.uber.org/zap"
)

// Resource defines an endpoint where artifacts can be stored, such as images.
type Resource struct {
	Name           string                                `json:"name"`
	Type           resourcev1alpha1.PipelineResourceType `json:"type"`
	URL            string                                `json:"url"`
	Digest         string                                `json:"digest"`
	OutputImageDir string
}

// NewResource creates a new ImageResource from a PipelineResourcev1alpha1.
func NewResource(name string, r *resourcev1alpha1.PipelineResource) (*Resource, error) {
	if r.Spec.Type != resourcev1alpha1.PipelineResourceTypeImage {
		return nil, fmt.Errorf("ImageResource: Cannot create an Image resource from a %s Pipeline Resource", r.Spec.Type)
	}
	ir := &Resource{
		Name: name,
		Type: resourcev1alpha1.PipelineResourceTypeImage,
	}

	for _, param := range r.Spec.Params {
		switch {
		case strings.EqualFold(param.Name, "URL"):
			ir.URL = param.Value
		case strings.EqualFold(param.Name, "Digest"):
			ir.Digest = param.Value
		}
	}

	return ir, nil
}

// GetName returns the name of the resource
func (s Resource) GetName() string {
	return s.Name
}

// GetType returns the type of the resource, in this case "image"
func (s Resource) GetType() resourcev1alpha1.PipelineResourceType {
	return resourcev1alpha1.PipelineResourceTypeImage
}

// Replacements is used for template replacement on an ImageResource inside of a Taskrun.
func (s *Resource) Replacements() map[string]string {
	return map[string]string{
		"name":   s.Name,
		"type":   s.Type,
		"url":    s.URL,
		"digest": s.Digest,
	}
}

// GetInputTaskModifier returns the TaskModifier to be used when this resource is an input.
func (s *Resource) GetInputTaskModifier(_ *pipelinev1beta1.TaskSpec, _ string) (pipelinev1beta1.TaskModifier, error) {
	return &pipelinev1beta1.InternalTaskModifier{}, nil
}

// GetOutputTaskModifier returns a No-op TaskModifier.
func (s *Resource) GetOutputTaskModifier(_ *pipelinev1beta1.TaskSpec, _ string) (pipelinev1beta1.TaskModifier, error) {
	return &pipelinev1beta1.InternalTaskModifier{}, nil
}

func (s Resource) String() string {
	// the String() func implements the Stringer interface, and therefore
	// cannot return an error
	// if the Marshal func gives an error, the returned string will be empty
	json, _ := json.Marshal(s)
	return string(json)
}

func (s Resource) AttachSignature(signer *signing.Signer, tr *pipelinev1alpha1.TaskRun, l *zap.SugaredLogger) error {

	creds, err := k8schain.NewInCluster(k8schain.Options{
		Namespace:          "tekton-pipelines",
		ServiceAccountName: "tekton-pipelines-controller",
	})
	if err != nil {
		return err
	}

	rrs := map[string]string{}
	for _, rr := range tr.Status.ResourcesResult {
		if rr.ResourceRef.Name == s.Name {
			rrs[rr.Key] = rr.Value
		}
	}

	sig := SimpleSigning{
		Critical: Critical{
			Identity: Identity{
				DockerReference: s.URL,
			},
			Image: Image{
				DockerManifestDigest: rrs["digest"],
			},
			Type: "Tekton builder signature",
		},
		Optional: map[string]interface{}{
			"builder":    fmt.Sprintf("Tekton %s", version.PipelineVersion),
			"provenance": tr.Status,
		},
	}

	body, err := json.Marshal(sig)
	if err != nil {
		return err
	}

	l.Infof("Attaching signature %s to image %s", string(body), s.Name)

	signature, _, err := signer.Sign(sig)
	if err != nil {
		return err
	}

	tag, err := name.ParseReference(s.URL)
	if err != nil {
		return err
	}

	orig, err := remote.Image(tag, remote.WithAuthFromKeychain(creds))
	if err != nil {
		return err
	}

	dgst, err := orig.Digest()
	if err != nil {
		return err
	}

	signatureTar := bytes.Buffer{}
	w := tar.NewWriter(&signatureTar)
	w.WriteHeader(&tar.Header{
		Name: "signature",
		Size: int64(len(signature)),
		Mode: 0755,
	})
	w.Write(signature)
	w.WriteHeader(&tar.Header{
		Name: "body.json",
		Size: int64(len(body)),
		Mode: 0755,
	})
	w.Write(body)
	w.Close()

	// Now make the fake image to contain the signature object.
	layer, err := tarball.LayerFromReader(&signatureTar)
	if err != nil {
		return err
	}

	// Push it to registry/repository:$digest.sig
	signatureTag, err := name.ParseReference(fmt.Sprintf("%s/%s:%s.sig", tag.Context().RegistryStr(), tag.Context().RepositoryStr(), dgst.Hex))
	if err != nil {
		return err
	}

	signatureImg, err := mutate.AppendLayers(empty.Image, layer)
	if err != nil {
		return err
	}
	l.Infof("Pushing signature to %s", signatureTag)
	if err := remote.Write(signatureTag, signatureImg, remote.WithAuthFromKeychain(creds)); err != nil {
		return err
	}
	return nil
}

type Critical struct {
	Identity Identity `json:"identity`
	Image    Image    `json:"image"`
	Type     string   `json:"type"`
}

type Identity struct {
	DockerReference string `json:"docker-reference"`
}

type Image struct {
	DockerManifestDigest string `json:"Docker-manifest-digest"`
}

type SimpleSigning struct {
	Critical Critical
	Optional map[string]interface{}
}

type MySignature struct {
	mediaType string
	body      []byte
}

func (s *MySignature) MediaType() (types.MediaType, error) {
	return types.DockerManifestSchema2, nil
}
func (s *MySignature) Digest() (v1.Hash, error) {
	digest, _, err := v1.SHA256(bytes.NewReader(s.body))
	return digest, err
}
func (s *MySignature) Size() (int64, error) {
	return int64(len(s.body)), nil
}
