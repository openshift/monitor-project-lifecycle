// This file was automatically generated by informer-gen

package v1

import (
	user_v1 "github.com/openshift/api/user/v1"
	versioned "github.com/openshift/client-go/user/clientset/versioned"
	internalinterfaces "github.com/openshift/client-go/user/informers/externalversions/internalinterfaces"
	v1 "github.com/openshift/client-go/user/listers/user/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	watch "k8s.io/apimachinery/pkg/watch"
	cache "k8s.io/client-go/tools/cache"
	time "time"
)

// IdentityInformer provides access to a shared informer and lister for
// Identities.
type IdentityInformer interface {
	Informer() cache.SharedIndexInformer
	Lister() v1.IdentityLister
}

type identityInformer struct {
	factory          internalinterfaces.SharedInformerFactory
	tweakListOptions internalinterfaces.TweakListOptionsFunc
}

// NewIdentityInformer constructs a new informer for Identity type.
// Always prefer using an informer factory to get a shared informer instead of getting an independent
// one. This reduces memory footprint and number of connections to the server.
func NewIdentityInformer(client versioned.Interface, resyncPeriod time.Duration, indexers cache.Indexers) cache.SharedIndexInformer {
	return NewFilteredIdentityInformer(client, resyncPeriod, indexers, nil)
}

// NewFilteredIdentityInformer constructs a new informer for Identity type.
// Always prefer using an informer factory to get a shared informer instead of getting an independent
// one. This reduces memory footprint and number of connections to the server.
func NewFilteredIdentityInformer(client versioned.Interface, resyncPeriod time.Duration, indexers cache.Indexers, tweakListOptions internalinterfaces.TweakListOptionsFunc) cache.SharedIndexInformer {
	return cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options meta_v1.ListOptions) (runtime.Object, error) {
				if tweakListOptions != nil {
					tweakListOptions(&options)
				}
				return client.UserV1().Identities().List(options)
			},
			WatchFunc: func(options meta_v1.ListOptions) (watch.Interface, error) {
				if tweakListOptions != nil {
					tweakListOptions(&options)
				}
				return client.UserV1().Identities().Watch(options)
			},
		},
		&user_v1.Identity{},
		resyncPeriod,
		indexers,
	)
}

func (f *identityInformer) defaultInformer(client versioned.Interface, resyncPeriod time.Duration) cache.SharedIndexInformer {
	return NewFilteredIdentityInformer(client, resyncPeriod, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}, f.tweakListOptions)
}

func (f *identityInformer) Informer() cache.SharedIndexInformer {
	return f.factory.InformerFor(&user_v1.Identity{}, f.defaultInformer)
}

func (f *identityInformer) Lister() v1.IdentityLister {
	return v1.NewIdentityLister(f.Informer().GetIndexer())
}
