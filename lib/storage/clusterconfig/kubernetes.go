/*
Copyright 2020 Gravitational, Inc.

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

package clusterconfig

import (
	"context"
	"encoding/json"

	"github.com/gravitational/gravity/lib/constants"
	"github.com/gravitational/gravity/lib/defaults"
	kuberneteslib "github.com/gravitational/gravity/lib/kubernetes"

	"github.com/gravitational/rigging"
	"github.com/gravitational/trace"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// ServiceControl provides the interface to get and update the controller
// service.
type ServiceControl interface {
	// Get returns the controller service configuration.
	// Returns NotFound if the controller service is not found.
	Get() (*GravityControllerService, error)
	// Update updates the controller service.
	Update(*GravityControllerService) error
}

type serviceControl struct {
	*kubernetes.Clientset
}

// NewServiceControl returns a new ServiceControl using the provided
// kubernetes client.
func NewServiceControl(client *kubernetes.Clientset) ServiceControl {
	return &serviceControl{
		Clientset: client,
	}
}

// Get returns the controller service configuration. Returns NotFound if the
// controller service is not found.
func (r *serviceControl) Get() (*GravityControllerService, error) {
	controllerSvc, err := r.CoreV1().
		Services(defaults.KubeSystemNamespace).
		Get(constants.GravityServiceName, metav1.GetOptions{})
	if err := rigging.ConvertError(err); err != nil {
		return nil, trace.Wrap(err)
	}
	return toServiceConfig(controllerSvc), nil
}

// toServiceConfig returns the kubernetes service as a GravityControllerService.
func toServiceConfig(svc *v1.Service) *GravityControllerService {
	if svc == nil {
		return nil
	}
	return &GravityControllerService{
		Labels:      svc.GetLabels(),
		Annotations: svc.GetAnnotations(),
		Spec: ControllerServiceSpec{
			Type:  string(svc.Spec.Type),
			Ports: toPorts(svc.Spec.Ports),
		},
	}
}

// Update updates the controller service using the provided config.
func (r *serviceControl) Update(config *GravityControllerService) error {
	services := r.CoreV1().Services(defaults.KubeSystemNamespace)

	existingService, err := services.Get(constants.GravityServiceName, metav1.GetOptions{})
	err = rigging.ConvertError(err)
	if err != nil && !trace.IsNotFound(err) {
		return trace.Wrap(err)
	}

	// Initialize new controller service if not found.
	if trace.IsNotFound(err) {
		newService := toService(config)
		if newService == nil {
			newService = ControllerService()
		}
		_, err = services.Create(newService)
		if err = rigging.ConvertError(err); err != nil {
			return trace.Wrap(err)
		}
		return nil
	}

	updatedService := toService(config)
	if !shouldUpdate(existingService, updatedService) {
		return nil
	}

	if _, err := services.Update(updatedService); err != nil {
		return trace.Wrap(err)
	}

	return nil
}

// toService returns a kubernetes service constructed from the provided config.
// Returns nil if config is empty.
func toService(config *GravityControllerService) *v1.Service {
	if config.IsEmpty() {
		return nil
	}
	updatedService := ControllerService()
	if len(config.Labels) != 0 {
		updatedService.Labels = config.Labels
	}
	if len(config.Annotations) != 0 {
		updatedService.Annotations = config.Annotations
	}
	if config.Spec.Type != "" {
		updatedService.Spec.Type = v1.ServiceType(config.Spec.Type)
	}
	if len(config.Spec.Ports) != 0 {
		updatedService.Spec.Ports = toServicePorts(config.Spec.Ports)
	}
	return updatedService
}

// shouldUpdate returns true if the two provided services have diverged.
// Returns false if updated service is nil.
func shouldUpdate(existing, updated *v1.Service) bool {
	if updated == nil {
		return false
	}
	if len(existing.Labels) != len(updated.Labels) {
		return true
	}
	for key, updatedVal := range updated.Labels {
		existingVal, exists := existing.Labels[key]
		if !exists || existingVal != updatedVal {
			return true
		}
	}

	if len(existing.Annotations) != len(updated.Annotations) {
		return true
	}
	for key, updatedVal := range updated.Annotations {
		existingVal, exists := existing.Annotations[key]
		if !exists || existingVal != updatedVal {
			return true
		}
	}

	if existing.Spec.Type != updated.Spec.Type {
		return true
	}

	if len(existing.Spec.Ports) != len(updated.Spec.Ports) {
		return true
	}
	for i, updatedPort := range updated.Spec.Ports {
		existingPort := existing.Spec.Ports[i]
		if existingPort != updatedPort {
			return true
		}
	}

	return false
}

// ClusterConfigControl provides an interface to interact with the cluster
// configuration resource.
type ClusterConfigControl interface {
	// Get returns the cluster's ClusterConfiguration resource.
	// Returns NotFound if cluster configmap is not found.
	Get() (*Resource, error)
	// Update updates the cluster's ClusterConfiguration resource.
	Update(*Resource) error
}

type clusterConfigControl struct {
	*kubernetes.Clientset
}

// NewClusterConfigControl returns a new ClusterConfigControl using the provided
// kubernetes client.
func NewClusterConfigControl(client *kubernetes.Clientset) ClusterConfigControl {
	return &clusterConfigControl{
		Clientset: client,
	}
}

// Get returns the cluster configuration. Returns NotFound if the cluster
// configmap is not found.
func (r *clusterConfigControl) Get() (*Resource, error) {
	configmap, err := r.CoreV1().
		ConfigMaps(defaults.KubeSystemNamespace).
		Get(constants.ClusterConfigurationMap, metav1.GetOptions{})

	if err := rigging.ConvertError(err); err != nil {
		return nil, trace.Wrap(err)
	}

	spec := configmap.Data["spec"]
	if spec == "" {
		return nil, trace.NotFound("cluster spec is empty")
	}

	config, err := Unmarshal([]byte(spec))
	if err != nil {
		return nil, trace.Wrap(err)
	}

	return config, nil
}

// Update updates the cluster configuration with the provided config values.
func (r *clusterConfigControl) Update(config *Resource) error {
	configmaps := r.CoreV1().ConfigMaps(defaults.KubeSystemNamespace)

	configmap, err := configmaps.Get(constants.ClusterConfigurationMap, metav1.GetOptions{})
	err = rigging.ConvertError(err)
	if err != nil && !trace.IsNotFound(err) {
		return trace.Wrap(err)
	}

	// Initialize new cluster configuration configmap if not found.
	if trace.IsNotFound(err) {
		configmap, err = configmaps.Create(ClusterConfigMap())
		if err != nil {
			return trace.Wrap(rigging.ConvertError(err))
		}
	}

	// Record previous key/values.
	if len(configmap.Data) != 0 {
		previousKeyValues, err := json.Marshal(configmap.Data)
		if err != nil {
			return trace.Wrap(err, "failed to marshal previous key/values")
		}
		configmap.Annotations[constants.PreviousKeyValuesAnnotationKey] = string(previousKeyValues)
	}

	spec, err := Marshal(config)
	if err != nil {
		return trace.Wrap(err)
	}

	configmap.Data = map[string]string{
		"spec": string(spec),
	}

	err = kuberneteslib.Retry(context.TODO(), func() error {
		_, err := configmaps.Update(configmap)
		return trace.Wrap(err)
	})

	return trace.Wrap(err)
}

// Reconcile reconciles current controller service with the desired state.
func Reconcile(clusterControl ClusterConfigControl, serviceControl ServiceControl) error {
	clusterConfig, err := clusterControl.Get()
	if err != nil && !trace.IsNotFound(err) {
		return trace.Wrap(err)
	}

	var serviceConfig *GravityControllerService
	if !trace.IsNotFound(err) {
		serviceConfig = clusterConfig.GetGravityControllerServiceConfig()
	}

	if err := serviceControl.Update(serviceConfig); err != nil {
		return trace.Wrap(err)
	}

	return nil
}
