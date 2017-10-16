// This file was automatically generated by lister-gen

package v1

import (
	v1 "github.com/openshift/api/oauth/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"
)

// OAuthClientLister helps list OAuthClients.
type OAuthClientLister interface {
	// List lists all OAuthClients in the indexer.
	List(selector labels.Selector) (ret []*v1.OAuthClient, err error)
	// Get retrieves the OAuthClient from the index for a given name.
	Get(name string) (*v1.OAuthClient, error)
	OAuthClientListerExpansion
}

// oAuthClientLister implements the OAuthClientLister interface.
type oAuthClientLister struct {
	indexer cache.Indexer
}

// NewOAuthClientLister returns a new OAuthClientLister.
func NewOAuthClientLister(indexer cache.Indexer) OAuthClientLister {
	return &oAuthClientLister{indexer: indexer}
}

// List lists all OAuthClients in the indexer.
func (s *oAuthClientLister) List(selector labels.Selector) (ret []*v1.OAuthClient, err error) {
	err = cache.ListAll(s.indexer, selector, func(m interface{}) {
		ret = append(ret, m.(*v1.OAuthClient))
	})
	return ret, err
}

// Get retrieves the OAuthClient from the index for a given name.
func (s *oAuthClientLister) Get(name string) (*v1.OAuthClient, error) {
	key := &v1.OAuthClient{ObjectMeta: meta_v1.ObjectMeta{Name: name}}
	obj, exists, err := s.indexer.Get(key)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, errors.NewNotFound(v1.Resource("oauthclient"), name)
	}
	return obj.(*v1.OAuthClient), nil
}
