/*
Copyright 2017 the Heptio Ark contributors.

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
package v1

import (
	v1 "github.com/heptio/ark/pkg/apis/ark/v1"
	scheme "github.com/heptio/ark/pkg/generated/clientset/versioned/scheme"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	types "k8s.io/apimachinery/pkg/types"
	watch "k8s.io/apimachinery/pkg/watch"
	rest "k8s.io/client-go/rest"
)

// DownloadRequestsGetter has a method to return a DownloadRequestInterface.
// A group's client should implement this interface.
type DownloadRequestsGetter interface {
	DownloadRequests(namespace string) DownloadRequestInterface
}

// DownloadRequestInterface has methods to work with DownloadRequest resources.
type DownloadRequestInterface interface {
	Create(*v1.DownloadRequest) (*v1.DownloadRequest, error)
	Update(*v1.DownloadRequest) (*v1.DownloadRequest, error)
	UpdateStatus(*v1.DownloadRequest) (*v1.DownloadRequest, error)
	Delete(name string, options *meta_v1.DeleteOptions) error
	DeleteCollection(options *meta_v1.DeleteOptions, listOptions meta_v1.ListOptions) error
	Get(name string, options meta_v1.GetOptions) (*v1.DownloadRequest, error)
	List(opts meta_v1.ListOptions) (*v1.DownloadRequestList, error)
	Watch(opts meta_v1.ListOptions) (watch.Interface, error)
	Patch(name string, pt types.PatchType, data []byte, subresources ...string) (result *v1.DownloadRequest, err error)
	DownloadRequestExpansion
}

// downloadRequests implements DownloadRequestInterface
type downloadRequests struct {
	client rest.Interface
	ns     string
}

// newDownloadRequests returns a DownloadRequests
func newDownloadRequests(c *ArkV1Client, namespace string) *downloadRequests {
	return &downloadRequests{
		client: c.RESTClient(),
		ns:     namespace,
	}
}

// Get takes name of the downloadRequest, and returns the corresponding downloadRequest object, and an error if there is any.
func (c *downloadRequests) Get(name string, options meta_v1.GetOptions) (result *v1.DownloadRequest, err error) {
	result = &v1.DownloadRequest{}
	err = c.client.Get().
		Namespace(c.ns).
		Resource("downloadrequests").
		Name(name).
		VersionedParams(&options, scheme.ParameterCodec).
		Do().
		Into(result)
	return
}

// List takes label and field selectors, and returns the list of DownloadRequests that match those selectors.
func (c *downloadRequests) List(opts meta_v1.ListOptions) (result *v1.DownloadRequestList, err error) {
	result = &v1.DownloadRequestList{}
	err = c.client.Get().
		Namespace(c.ns).
		Resource("downloadrequests").
		VersionedParams(&opts, scheme.ParameterCodec).
		Do().
		Into(result)
	return
}

// Watch returns a watch.Interface that watches the requested downloadRequests.
func (c *downloadRequests) Watch(opts meta_v1.ListOptions) (watch.Interface, error) {
	opts.Watch = true
	return c.client.Get().
		Namespace(c.ns).
		Resource("downloadrequests").
		VersionedParams(&opts, scheme.ParameterCodec).
		Watch()
}

// Create takes the representation of a downloadRequest and creates it.  Returns the server's representation of the downloadRequest, and an error, if there is any.
func (c *downloadRequests) Create(downloadRequest *v1.DownloadRequest) (result *v1.DownloadRequest, err error) {
	result = &v1.DownloadRequest{}
	err = c.client.Post().
		Namespace(c.ns).
		Resource("downloadrequests").
		Body(downloadRequest).
		Do().
		Into(result)
	return
}

// Update takes the representation of a downloadRequest and updates it. Returns the server's representation of the downloadRequest, and an error, if there is any.
func (c *downloadRequests) Update(downloadRequest *v1.DownloadRequest) (result *v1.DownloadRequest, err error) {
	result = &v1.DownloadRequest{}
	err = c.client.Put().
		Namespace(c.ns).
		Resource("downloadrequests").
		Name(downloadRequest.Name).
		Body(downloadRequest).
		Do().
		Into(result)
	return
}

// UpdateStatus was generated because the type contains a Status member.
// Add a +genclient:noStatus comment above the type to avoid generating UpdateStatus().

func (c *downloadRequests) UpdateStatus(downloadRequest *v1.DownloadRequest) (result *v1.DownloadRequest, err error) {
	result = &v1.DownloadRequest{}
	err = c.client.Put().
		Namespace(c.ns).
		Resource("downloadrequests").
		Name(downloadRequest.Name).
		SubResource("status").
		Body(downloadRequest).
		Do().
		Into(result)
	return
}

// Delete takes name of the downloadRequest and deletes it. Returns an error if one occurs.
func (c *downloadRequests) Delete(name string, options *meta_v1.DeleteOptions) error {
	return c.client.Delete().
		Namespace(c.ns).
		Resource("downloadrequests").
		Name(name).
		Body(options).
		Do().
		Error()
}

// DeleteCollection deletes a collection of objects.
func (c *downloadRequests) DeleteCollection(options *meta_v1.DeleteOptions, listOptions meta_v1.ListOptions) error {
	return c.client.Delete().
		Namespace(c.ns).
		Resource("downloadrequests").
		VersionedParams(&listOptions, scheme.ParameterCodec).
		Body(options).
		Do().
		Error()
}

// Patch applies the patch and returns the patched downloadRequest.
func (c *downloadRequests) Patch(name string, pt types.PatchType, data []byte, subresources ...string) (result *v1.DownloadRequest, err error) {
	result = &v1.DownloadRequest{}
	err = c.client.Patch(pt).
		Namespace(c.ns).
		Resource("downloadrequests").
		SubResource(subresources...).
		Name(name).
		Body(data).
		Do().
		Into(result)
	return
}
