package main

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"os/exec"

	"github.com/nshafer/phx"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func main() {
	log.Println("Starting the application...")
	clusterId := os.Getenv("CLUSTER_ID")
	if clusterId == "" {
		log.Fatal("CLUSTER_ID environment variable is not set")
	}

	clusterSecret := os.Getenv("CLUSTER_SECRET")
	if clusterSecret == "" {
		log.Fatal("CLUSTER_SECRET environment variable is not set")
	}

	backendHost := os.Getenv("BACKEND_HOST")
	if backendHost == "" {
		log.Fatal("BACKEND_HOST environment variable is not set")
	}

	backendTLS, _ := strconv.ParseBool(os.Getenv("BACKEND_TLS"))
	if os.Getenv("BACKEND_TLS") == "" || os.Getenv("BACKEND_TLS") == "true" {
		backendTLS = true // Default to true if not set
	}

	endpointURL := fmt.Sprintf("%s://%s/socket/kubernetes/clusters", "wss", backendHost)
	if !backendTLS {
		endpointURL = fmt.Sprintf("%s://%s/socket/kubernetes/clusters", "ws", backendHost)
	}

	config, err := rest.InClusterConfig()
	if err != nil {
		log.Fatal("Failed to get in-cluster config:", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatal("Failed to create Kubernetes client:", err)
	}

	log.Printf("Connecting to WebSocket at %s", endpointURL)
	endPoint, err := url.Parse(endpointURL)
	if err != nil {
		log.Fatal("Failed to parse WebSocket URL:", err)
	}

	socket := phx.NewSocket(endPoint)
	err = socket.Connect()
	if err != nil {
		log.Fatal("Failed to connect to socket:", err)
	}
	log.Println("Successfully connected to WebSocket")

	log.Printf("Joining channel: cluster:%s:%s", clusterId, clusterSecret)
	channel := socket.Channel(fmt.Sprintf("cluster:%s:%s", clusterId, clusterSecret), nil)
	join, err := channel.Join()
	if err != nil {
		log.Fatal("Failed to join channel:", err)
	}

	join.Receive("ok", func(response any) {
		log.Println("Joined channel:", channel.Topic(), response)
		go sendClusterInfo(clientset, channel)
		go watchIngresses(clientset, channel)
		go watchServices(clientset, channel)
	})

	// Setup owner reference for the rest
	updateOwnerReference("serviceaccount", "anylb-k8s-agent")
	updateOwnerReference("clusterrole", "anylb-k8s-agent-role")
	updateOwnerReference("clusterrolebinding", "anylb-k8s-agent-role-binding")
	updateOwnerReference("secret", "anylb-k8s-agent-secret")

	// Apply WireGuard
	applyManifest(getManifestURL(backendHost, backendTLS, "wireguard", clusterId, clusterSecret), "wireguard")
	updateOwnerReference("configmap", "anylb-wireguard-config")
	updateOwnerReference("deployment", "anylb-wireguard")

	channel.On("uninstall", func(payload any) {
		log.Println("Uninstall triggered by website")

		// Remove the anylb-k8s-agent deployment
		cmd := exec.Command("kubectl", "delete", "deployment", "anylb-k8s-agent")
		output, err := cmd.CombinedOutput()
		if err != nil {
			log.Printf("Failed to remove anylb-k8s-agent deployment: %v\nOutput:\n%s", err, string(output))
		} else {
			log.Printf("Successfully removed anylb-k8s-agent deployment. Output: %s", string(output))
		}

		os.Exit(0)
	})

	channel.On("publish", func(payload any) {
		handlePublishService(clientset, payload)
	})

	channel.On("unpublish", func(payload any) {
		handleUnpublishService(clientset, payload)
	})

	log.Println("Main loop started. Waiting for events...")
	log.Println("Main loop started. Waiting for events...")
	select {} // Keep the program running
}

func watchIngresses(clientset *kubernetes.Clientset, channel *phx.Channel) {
	go sendAllIngresses(clientset, channel)

	watcher, err := clientset.NetworkingV1().Ingresses("").Watch(context.Background(), metav1.ListOptions{})
	if err != nil {
		log.Fatal("Failed to create ingress watcher:", err)
	}

	for event := range watcher.ResultChan() {
		ingress, ok := event.Object.(*networkingv1.Ingress)
		if !ok {
			log.Println("Unexpected object type in watch event")
			continue
		}

		switch event.Type {
		case watch.Added, watch.Modified:
			handleIngressUpsert(channel, ingress)
		case watch.Deleted:
			handleIngressDelete(channel, ingress)
		}
	}
}

func handleIngressUpsert(channel *phx.Channel, ingress *networkingv1.Ingress) {
	// Check if the Ingress has been assigned a LoadBalancer
	if !hasLoadBalancer(ingress) {
		ingressData := serializeIngress(ingress)
		push, err := channel.Push("ingress/upsert", ingressData)
		if err != nil {
			log.Println("Failed to send ingress upsert:", err)
			return
		}

		push.Receive("ok", func(response any) {
			log.Println("Ingress upsert sent successfully:", response)
		})
	} else {
		log.Printf("Ingress %s/%s already has a LoadBalancer, skipping upsert", ingress.Namespace, ingress.Name)
	}
}

func hasLoadBalancer(ingress *networkingv1.Ingress) bool {
	for _, lbIngress := range ingress.Status.LoadBalancer.Ingress {
		if lbIngress.IP != "" || lbIngress.Hostname != "" {
			return true
		}
	}
	return false
}

func handleIngressDelete(channel *phx.Channel, ingress *networkingv1.Ingress) {
	deleteData := map[string]interface{}{
		"id": string(ingress.UID),
	}

	push, err := channel.Push("ingress/delete", deleteData)
	if err != nil {
		log.Println("Failed to send ingress delete:", err)
		return
	}

	push.Receive("ok", func(response any) {
		log.Println("Ingress delete sent successfully:", response)
	})
}

func sendClusterInfo(clientset *kubernetes.Clientset, channel *phx.Channel) {
	// Create a watcher for nodes
	watcher, err := clientset.CoreV1().Nodes().Watch(context.Background(), metav1.ListOptions{})
	if err != nil {
		log.Fatal("Failed to create node watcher:", err)
	}
	defer watcher.Stop()

	// Channel to trigger updates
	updateTrigger := make(chan struct{}, 1)

	// Start a goroutine to watch for node events
	go func() {
		for event := range watcher.ResultChan() {
			switch event.Type {
			case watch.Added, watch.Modified, watch.Deleted:
				// Trigger an update
				select {
				case updateTrigger <- struct{}{}:
				default:
					// Channel already has an update pending, no need to add another
				}
			}
		}
	}()

	// Function to send cluster info
	sendUpdate := func() {
		nodes, err := clientset.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
		if err != nil {
			log.Println("Failed to list nodes:", err)
			return
		}

		nodeInfos := make([]map[string]interface{}, 0, len(nodes.Items))
		for _, node := range nodes.Items {
			nodeInfo := extractNodeInfo(&node)
			nodeInfos = append(nodeInfos, nodeInfo)
		}

		clusterInfo := map[string]interface{}{
			"nodes": nodeInfos,
		}

		push, err := channel.Push("cluster/info", clusterInfo)
		if err != nil {
			log.Println("Failed to send cluster info:", err)
		} else {
			push.Receive("ok", func(response any) {
				log.Println("Cluster info sent successfully:", response)
			})
		}
	}

	// Send initial update
	sendUpdate()

	// Main loop
	for {
		select {
		case <-updateTrigger:
			sendUpdate()
		case <-time.After(5 * time.Minute):
			// Send periodic updates every 5 minutes
			sendUpdate()
		}
	}
}

func extractNodeInfo(node *corev1.Node) map[string]interface{} {
	nodeInfo := map[string]interface{}{
		"id":         string(node.UID),
		"name":       node.Name,
		"status":     string(node.Status.Phase),
		"ready":      isNodeReady(node),
		"os":         node.Status.NodeInfo.OperatingSystem,
		"kernel":     node.Status.NodeInfo.KernelVersion,
		"arch":       node.Status.NodeInfo.Architecture,
		"runtime":    node.Status.NodeInfo.KubeletVersion,
		"kubelet":    node.Status.NodeInfo.KubeletVersion,
		"kubeproxy":  node.Status.NodeInfo.KubeProxyVersion,
		"addresses":  extractNodeAddresses(node),
		"conditions": extractNodeConditions(node),
	}

	if len(node.Spec.PodCIDRs) > 0 {
		podCIDRs := make([]map[string]string, 0, len(node.Spec.PodCIDRs))
		for _, cidr := range node.Spec.PodCIDRs {
			podCIDRs = append(podCIDRs, map[string]string{
				"address": cidr,
				"type":    getPodCIDRType(node, cidr),
			})
		}
		nodeInfo["pod_cidrs"] = podCIDRs
	}

	// Detect underlying OS
	underlyingOS := detectUnderlyingOS()
	if underlyingOS != "" {
		nodeInfo["underlying_os"] = underlyingOS
	}

	return nodeInfo
}

func isNodeReady(node *corev1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

func extractNodeAddresses(node *corev1.Node) []map[string]string {
	addresses := make([]map[string]string, 0, len(node.Status.Addresses))
	for _, address := range node.Status.Addresses {
		addresses = append(addresses, map[string]string{
			"type":    string(address.Type),
			"address": address.Address,
		})
	}
	return addresses
}

func extractNodeConditions(node *corev1.Node) []map[string]interface{} {
	conditions := make([]map[string]interface{}, 0, len(node.Status.Conditions))
	for _, condition := range node.Status.Conditions {
		conditions = append(conditions, map[string]interface{}{
			"type":            string(condition.Type),
			"status":          string(condition.Status),
			"last_heartbeat":  condition.LastHeartbeatTime.String(),
			"last_transition": condition.LastTransitionTime.String(),
			"reason":          condition.Reason,
			"message":         condition.Message,
		})
	}
	return conditions
}

func getManifestURL(backendHost string, backendTLS bool, manifestType, clusterId, clusterSecret string) string {
	scheme := "https"
	if !backendTLS {
		scheme = "http"
	}
	manifestURL := fmt.Sprintf("%s://%s/kubernetes/%s/%s/%s", scheme, backendHost, manifestType, clusterId, clusterSecret)
	return manifestURL
}

func applyManifest(manifestURL, deploymentName string) {
	// Apply the manifest
	log.Printf("Applying manifest from %s", manifestURL)
	cmd := exec.Command("kubectl", "apply", "-f", manifestURL)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Failed to apply %s manifest: %v\nOutput:\n%s", deploymentName, err, string(output))
		return
	}
	log.Printf("Successfully applied %s manifest. Output: %s", deploymentName, string(output))

}

func updateOwnerReference(object, deploymentName string) {
	// Get the UID of the anylb-k8s-agent deployment
	agentUID, err := getDeploymentUID("anylb-k8s-agent")
	if err != nil {
		log.Printf("Failed to get anylb-k8s-agent UID: %v", err)
		return
	}

	// Update the owner reference of the newly created deployment
	updateCmd := exec.Command("kubectl", "patch", object, deploymentName,
		"--type=json",
		"-p", fmt.Sprintf(`[{"op": "add", "path": "/metadata/ownerReferences", "value": [{"apiVersion": "apps/v1", "kind": "Deployment", "name": "anylb-k8s-agent", "uid": "%s"}]}]`, agentUID))
	updateOutput, err := updateCmd.CombinedOutput()
	if err != nil {
		log.Printf("Failed to update owner reference for %s %s: %v\nOutput:\n%s", object, deploymentName, err, string(updateOutput))
		return
	}
	log.Printf("Successfully updated owner reference for %s %s. Output: %s", object, deploymentName, string(updateOutput))
}

// Add this new function to get the UID of a deployment
func getDeploymentUID(deploymentName string) (string, error) {
	cmd := exec.Command("kubectl", "get", "deployment", deploymentName, "-o", "jsonpath={.metadata.uid}")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get deployment UID: %v", err)
	}
	return string(output), nil
}

func serializeIngress(ingress *networkingv1.Ingress) map[string]interface{} {
	rules := make([]map[string]interface{}, 0)
	for _, rule := range ingress.Spec.Rules {
		paths := make([]map[string]interface{}, 0)
		if rule.HTTP != nil {
			for _, path := range rule.HTTP.Paths {
				pathMap := map[string]interface{}{
					"path":      path.Path,
					"path_type": string(*path.PathType),
					"backend": map[string]interface{}{
						"service_name": path.Backend.Service.Name,
						"service_port": path.Backend.Service.Port.Number,
					},
				}
				paths = append(paths, pathMap)
			}
		}

		ruleMap := map[string]interface{}{
			"host":  rule.Host,
			"paths": paths,
		}
		rules = append(rules, ruleMap)
	}

	return map[string]interface{}{
		"id":        string(ingress.UID),
		"name":      ingress.Name,
		"namespace": ingress.Namespace,
		"rules":     rules,
	}
}

// Add this new function to detect the underlying OS
func detectUnderlyingOS() string {
	cmd := exec.Command("kubectl", "run", "env-pod", "--image=busybox", "-it", "--rm", "--restart=Never", "--", "env")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}

	outputStr := string(output)
	if strings.Contains(strings.ToLower(outputStr), "darwin") || strings.Contains(strings.ToLower(outputStr), "mac") {
		return "macOS"
	} else if strings.Contains(strings.ToLower(outputStr), "windows") {
		return "Windows"
	}

	return ""
}

func getPodCIDRType(node *corev1.Node, cidr string) string {
	// Check if the CIDR matches any of the node's addresses
	for _, address := range node.Status.Addresses {
		if strings.HasPrefix(address.Address, strings.Split(cidr, "/")[0]) {
			return string(address.Type)
		}
	}

	// If no match is found, default to "InternalIP" as it's the most common for pod CIDRs
	return "InternalIP"
}

// Updated function
func watchServices(clientset *kubernetes.Clientset, channel *phx.Channel) {
	go sendServices(clientset, channel)

	watcher, err := clientset.CoreV1().Services("").Watch(context.Background(), metav1.ListOptions{})
	if err != nil {
		log.Fatal("Failed to create service watcher:", err)
	}

	for event := range watcher.ResultChan() {
		_, ok := event.Object.(*corev1.Service)
		if !ok {
			continue
		}

		if event.Type == watch.Added || event.Type == watch.Modified || event.Type == watch.Deleted {
			sendServices(clientset, channel)
		}
	}
}

func sendServices(clientset *kubernetes.Clientset, channel *phx.Channel) {
	services, err := clientset.CoreV1().Services("").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		log.Printf("Failed to list services: %v", err)
		return
	}

	servicesdefs := []map[string]interface{}{}

	for _, service := range services.Items {
		if service.Spec.Type == corev1.ServiceTypeClusterIP {
			serviceInfo := map[string]interface{}{
				"id":           string(service.UID),
				"namespace":    service.Namespace,
				"name":         service.Name,
				"type":         string(service.Spec.Type),
				"cluster_ip":   service.Spec.ClusterIP,
				"external_ips": service.Spec.ExternalIPs,
				"ports":        service.Spec.Ports,
			}
			servicesdefs = append(servicesdefs, serviceInfo)
		}
	}

	if len(servicesdefs) > 0 {
		push, err := channel.Push("cluster/services", servicesdefs)
		if err != nil {
			log.Printf("Failed to send services: %v", err)
			return
		}

		push.Receive("ok", func(response any) {
			log.Println("Services sent successfully:", response)
		})
	}
}

func handlePublishService(clientset *kubernetes.Clientset, payload any) {
	serviceInfo, ok := payload.(map[string]interface{})
	if !ok {
		log.Println("Invalid payload format for publish service")
		return
	}

	namespace, ok := serviceInfo["namespace"].(string)
	if !ok {
		log.Println("Invalid namespace in publish service payload")
		return
	}

	name, ok := serviceInfo["name"].(string)
	if !ok {
		log.Println("Invalid name in publish service payload")
		return
	}

	service, err := clientset.CoreV1().Services(namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		log.Printf("Failed to get service %s/%s: %v", namespace, name, err)
		return
	}

	externalIP, ok := serviceInfo["external_ip"].(string)
	if !ok {
		log.Println("Invalid external IP in publish service payload")
		return
	}

	service.Spec.ExternalIPs = []string{externalIP}

	_, err = clientset.CoreV1().Services(namespace).Update(context.Background(), service, metav1.UpdateOptions{})
	if err != nil {
		log.Printf("Failed to update Service %s/%s: %v", namespace, name, err)
	} else {
		log.Printf("Successfully updated Service %s/%s with ExternalIP %s", namespace, name, externalIP)
	}
}

func handleUnpublishService(clientset *kubernetes.Clientset, payload any) {
	serviceInfo, ok := payload.(map[string]interface{})
	if !ok {
		log.Println("Invalid payload format for unpublish service")
		return
	}

	namespace, ok := serviceInfo["namespace"].(string)
	if !ok {
		log.Println("Invalid namespace in unpublish service payload")
		return
	}

	name, ok := serviceInfo["name"].(string)
	if !ok {
		log.Println("Invalid name in unpublish service payload")
		return
	}

	service, err := clientset.CoreV1().Services(namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		log.Printf("Failed to get service %s/%s: %v", namespace, name, err)
		return
	}

	externalIP, ok := serviceInfo["external_ip"].(string)
	if !ok {
		log.Println("Invalid external IP in unpublish service payload")
		return
	}

	service.Spec.ExternalIPs = nil

	_, err = clientset.CoreV1().Services(namespace).Update(context.Background(), service, metav1.UpdateOptions{})
	if err != nil {
		log.Printf("Failed to update Service %s/%s: %v", namespace, name, err)
	} else {
		log.Printf("Successfully removed ExternalIP %s from Service %s/%s", externalIP, namespace, name)
	}

}

// Add this new function
func sendAllIngresses(clientset *kubernetes.Clientset, channel *phx.Channel) {
	ingresses, err := clientset.NetworkingV1().Ingresses("").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		log.Printf("Failed to list ingresses: %v", err)
		return
	}

	ingressDefs := []map[string]interface{}{}

	for _, ingress := range ingresses.Items {
		if !hasLoadBalancer(&ingress) {
			ingressData := serializeIngress(&ingress)
			ingressDefs = append(ingressDefs, ingressData)
		}
	}

	push, err := channel.Push("cluster/ingresses", ingressDefs)
	if err != nil {
		log.Printf("Failed to send ingresses: %v", err)
		return
	}

	push.Receive("ok", func(response any) {
		log.Println("Ingresses sent successfully:", response)
	})
}
