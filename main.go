package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/chrispyduck/kube-plex/pkg/signals"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

var namespace = os.Getenv("KUBE_NAMESPACE")
var currentPodName = os.Getenv("KUBE_POD_NAME")
var pmsInternalAddress = os.Getenv("PMS_INTERNAL_ADDRESS")

func main() {
	args := os.Args

	rewriteArgs(args)

	cfg, err := clientcmd.BuildConfigFromFlags("", "")
	if err != nil {
		log.Fatalf("Error building kubeconfig: %s", err)
	}

	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		log.Fatalf("Error building kubernetes clientset: %s", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		log.Fatalf("Error getting working directory: %s", err)
	}

	currentPod, err := kubeClient.CoreV1().Pods(namespace).Get(context.TODO(), currentPodName, metav1.GetOptions{})
	if err != nil {
		log.Fatalf("Error getting current pod: %s", err)
	}

	pod := generatePod(currentPod, cwd, args)

	createdPod, err := kubeClient.CoreV1().Pods(namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
	if err != nil {
		log.Fatalf("Error creating pod: %s", err)
	}
	log.Printf("Created pod: %s", createdPod.GetName())

	stopCh := signals.SetupSignalHandler()
	waitFn := func() <-chan error {
		stopCh := make(chan error)
		go func() {
			stopCh <- waitForPodCompletion(kubeClient, pod)
		}()
		return stopCh
	}

	select {
	case err := <-waitFn():
		if err != nil {
			log.Printf("Error waiting for pod to complete: %s", err)
		}
	case <-stopCh:
		log.Printf("Exit requested.")
	}

	log.Printf("Cleaning up pod...")
	err = kubeClient.CoreV1().Pods(namespace).Delete(context.TODO(), pod.Name, metav1.DeleteOptions{})
	if err != nil {
		log.Fatalf("Error cleaning up pod: %s", err)
	}
}

func rewriteArgs(in []string) {
	for i, v := range in {
		switch v {
		case "-progressurl", "-manifest_name", "-segment_list":
			in[i+1] = strings.Replace(in[i+1], "http://127.0.0.1:32400", pmsInternalAddress, 1)
		case "-loglevel", "-loglevel_plex":
			in[i+1] = "debug"
		}
	}
}

func generatePod(currentPod *corev1.Pod, cwd string, args []string) *corev1.Pod {
	//envVars := toCoreV1EnvVar(env)
	log.Printf("Creating pod to run command: %s", strings.Join(args, " "))
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: currentPod.ObjectMeta.Labels["app"] + "-transcoder-",
		},
		Spec: corev1.PodSpec{
			NodeSelector: map[string]string{
				"beta.kubernetes.io/arch": "amd64",
			},
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:         "plex",
					Command:      args,
					Image:        currentPod.Spec.Containers[0].Image,
					Env:          currentPod.Spec.Containers[0].Env,
					WorkingDir:   cwd,
					VolumeMounts: []corev1.VolumeMount{},
				},
			},
			Volumes: []corev1.Volume{},
		},
	}
	for _, currentPodVolumeMount := range currentPod.Spec.Containers[0].VolumeMounts {
		if currentPodVolumeMount.Name != "shared" && !strings.HasPrefix(currentPodVolumeMount.Name, "kube") {
			log.Printf("Adding volume mount for %s", currentPodVolumeMount.Name)
			pod.Spec.Containers[0].VolumeMounts = append(pod.Spec.Containers[0].VolumeMounts, currentPodVolumeMount)
		}
	}
	for _, currentPodVolume := range currentPod.Spec.Volumes {
		if currentPodVolume.Name != "shared" && !strings.HasPrefix(currentPodVolume.Name, "kube") {
			log.Printf("Adding volume for %s", currentPodVolume.Name)
			pod.Spec.Volumes = append(pod.Spec.Volumes, currentPodVolume)
		}
	}
	return pod
}

func toCoreV1EnvVar(in []string) []corev1.EnvVar {
	out := make([]corev1.EnvVar, len(in))
	for i, v := range in {
		splitvar := strings.SplitN(v, "=", 2)
		out[i] = corev1.EnvVar{
			Name:  splitvar[0],
			Value: splitvar[1],
		}
	}
	return out
}

func waitForPodCompletion(cl kubernetes.Interface, pod *corev1.Pod) error {
	for {
		pod, err := cl.CoreV1().Pods(pod.Namespace).Get(context.TODO(), pod.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}

		switch pod.Status.Phase {
		case corev1.PodPending:
		case corev1.PodRunning:
		case corev1.PodUnknown:
			log.Printf("Warning: pod %q is in an unknown state", pod.Name)
		case corev1.PodFailed:
			return fmt.Errorf("pod %q failed", pod.Name)
		case corev1.PodSucceeded:
			return nil
		}
		time.Sleep(1 * time.Second)
	}
}
