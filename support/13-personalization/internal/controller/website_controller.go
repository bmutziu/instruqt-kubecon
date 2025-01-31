/*
Copyright 2023 bmutziu.

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

package controller

import (
	"context"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	kubeconv1beta1 "github.com/bmutziu/instruqt-kubecon/api/v1beta1"
)

// WebsiteReconciler reconciles a Website object
type WebsiteReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=kubecon.bmutziu.me,resources=websites,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=kubecon.bmutziu.me,resources=websites/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=kubecon.bmutziu.me,resources=websites/finalizers,verbs=update

//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Website object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.14.4/pkg/reconcile
func (r *WebsiteReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// _ indicates an unused variable in Golang.
	// By naming the variable, you can use the pre-configured logging framework.
	log := log.FromContext(ctx)

	// Start by declaring the custom resource to be type "Website"
	customResource := &kubeconv1beta1.Website{}

	// Then retrieve from the cluster the resource that triggered this reconciliation.
	// Store these contents into an object used throughout reconciliation.
	err := r.Client.Get(context.Background(), req.NamespacedName, customResource)
	if err != nil {
		if errors.IsNotFound(err) {
			// If the resource is not found, that is OK. It just means the desired state is to
			// not have any resources for this Website, so we will need to delete them.
			log.Info(fmt.Sprintf(`Custom resource for website "%s" does not exist, deleting associated resources`, req.Name))

			// Now, try and delete the resource, catch any errors
			deployErr := r.Client.Delete(ctx, newDeployment(req.Name, req.Namespace, "n/a"))

			// Success for this delete is either:
			// 1. the delete is successful without error
			// 2. the resource already doesn't exist so delete can't take action
			if deployErr != nil && !errors.IsNotFound(deployErr) {
				// If any other error occurs, log it
				log.Error(deployErr, fmt.Sprintf(`Failed to delete deployment "%s"`, req.Name))
			}

			// repeat logic from the deployment delete
			serviceErr := r.Client.Delete(ctx, newService(req.Name, req.Namespace))
			if serviceErr != nil && !errors.IsNotFound(serviceErr) {
				log.Error(serviceErr, fmt.Sprintf(`Failed to delete service "%s"`, req.Name))
			}

			// If either the deploy or service deletes fail, the reconcile should fail
			if deployErr != nil || serviceErr != nil {
				return ctrl.Result{}, fmt.Errorf("%v/n%v", deployErr, serviceErr)
			}
			return ctrl.Result{}, nil
		} else {
			// If the resource does not match a "Website" resource type, return failure.
			log.Error(err, fmt.Sprintf(`Failed to retrieve custom resource "%s"`, req.Name))
			return ctrl.Result{}, err
		}
	}

	// log.Info(`Hello from your new website reconciler!`)
	// Use the `ImageTag` field from the website spec to personalise the log
	log.Info(fmt.Sprintf(`Hello from your new website reconciler with tag "%s"!`, customResource.Spec.ImageTag))

	// Attempt to create the deployment, check for already existing one and return error if it fails
	err = r.Client.Create(ctx, newDeployment(customResource.Name, customResource.Namespace, customResource.Spec.ImageTag))
	if err != nil {
		if errors.IsAlreadyExists(err) {
			log.Info(fmt.Sprintf(`Deployment for website "%s" already exists"`, customResource.Name))
			// TODO: handle deployment updates gracefully
		} else {
			log.Error(err, fmt.Sprintf(`Failed to create deployment for website "%s"`, customResource.Name))
			return ctrl.Result{}, err
		}
	}

	// Attempt to create the service, check for existing allocation and return error if it fails
	err = r.Client.Create(ctx, newService(customResource.Name, customResource.Namespace))
	if err != nil {
		if errors.IsInvalid(err) && strings.Contains(err.Error(), "provided port is already allocated") {
			log.Info(fmt.Sprintf(`Service for website "%s" already exists`, customResource.Name))
			// TODO: handle service updates gracefully
			// Retrieve the current deployment for this website
			deploymentNamespacedName := types.NamespacedName{
				Name:      customResource.Name,
				Namespace: customResource.Namespace,
			}
			deployment := appsv1.Deployment{}
			r.Client.Get(ctx, deploymentNamespacedName, &deployment)

			// Update can be based on any or all fields of the resource. In this simple operator, only
			// the imageTag field which is being provided by the custom resource will be validated.
			currentImage := deployment.Spec.Template.Spec.Containers[0].Image
			desiredImage := fmt.Sprintf("abangser/todo-local-storage:%s", customResource.Spec.ImageTag)

			if currentImage != desiredImage {
				log.Info(fmt.Sprintf(`Image tag has updated from "%s" to "%s"`, currentImage, desiredImage))

				// This operator only cares about the one field, it does not want
				// to alter any other changes that may be acceptable. Therefore,
				// this update will only patch the single field!
				patch := client.StrategicMergeFrom(deployment.DeepCopy())
				deployment.Spec.Template.Spec.Containers[0].Image = desiredImage
				patch.Data(&deployment)

				// Try and apply this patch, if it fails, return the failure
				err := r.Client.Patch(ctx, &deployment, patch)
				if err != nil {
					log.Error(err, fmt.Sprintf(`Failed to update deployment for website "%s"`, customResource.Name))
					return ctrl.Result{}, err
				}
			}
		} else {
			log.Error(err, fmt.Sprintf(`Failed to create service for website "%s"`, customResource.Name))
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *WebsiteReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kubeconv1beta1.Website{}).
		Complete(r)
}

// Create a single reference for labels as it is a reused variable
func setResourceLabels(name string) map[string]string {
	return map[string]string{
		"website": name,
		"type":    "Website",
	}
}

// Create a deployment with the correct field values. By creating this in a function,
// it can be reused by all lifecycle functions (create, update, delete).
func newDeployment(name, namespace, imageTag string) *appsv1.Deployment {
	replicas := int32(2)

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    setResourceLabels(name),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: setResourceLabels(name)},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: setResourceLabels(name)},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "nginx",
							// This is a publicly available container.  Note the use of
							//`imageTag` as defined by the original resource request spec.
							Image: fmt.Sprintf("abangser/todo-local-storage:%s", imageTag),
							Ports: []corev1.ContainerPort{{
								ContainerPort: 80,
							}},
						},
					},
				},
			},
		},
	}
}

// Create a service with the correct field values. By creating this in a function,
// it can be reused by all lifecycle functions (create, update, delete).
func newService(name, namespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    setResourceLabels(name),
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Port:     80,
					NodePort: 31000,
				},
			},
			Selector: setResourceLabels(name),
			Type:     corev1.ServiceTypeNodePort,
		},
	}
}
