package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"
)

type ResourceUsage struct {
	Name      string
	Namespace string
	CPU       string
	Memory    string
	Requests  map[string]string
	Limits    map[string]string
}

type PerformanceRecommendation struct {
	ResourceName   string
	Namespace      string
	Issue          string
	Recommendation string
}

type MetricsData struct {
	PodMetrics  map[string]*PodMetrics
	NodeMetrics map[string]*NodeMetrics
}

type PodMetrics struct {
	MaxCPU     int64
	MaxMemory  int64
	Namespace  string
	Containers map[string]*ContainerMetrics
}

type ContainerMetrics struct {
	MaxCPU    int64
	MaxMemory int64
}

type NodeMetrics struct {
	MaxCPU    int64
	MaxMemory int64
}

type DeploymentMetrics struct {
	Name              string
	Namespace         string
	Pods              []string
	MaxCPU            int64
	MaxMemory         int64
	AvgCPU            int64
	AvgMemory         int64
	TotalPods         int
	PodsWithoutLimits int
	Recommendations   []string
}

// sanitizeFilename removes or replaces characters that are not safe for filenames
func sanitizeFilename(name string) string {
	// Replace colons and other problematic characters with hyphens
	reg := regexp.MustCompile(`[^a-zA-Z0-9-_.]`)
	sanitized := reg.ReplaceAllString(name, "-")

	// Remove multiple consecutive hyphens
	reg = regexp.MustCompile(`-+`)
	sanitized = reg.ReplaceAllString(sanitized, "-")

	// Remove leading and trailing hyphens
	sanitized = strings.Trim(sanitized, "-")

	return sanitized
}

func checkMetricsServer(metricsClient *metricsv.Clientset) error {
	// Tentar listar métricas dos nodes para verificar se o Metrics Server está disponível
	_, err := metricsClient.MetricsV1beta1().NodeMetricses().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("erro ao conectar com o Metrics Server: %v\nCertifique-se de que o Metrics Server está instalado e funcionando no cluster", err)
	}
	return nil
}

func collectMetrics(clientset *kubernetes.Clientset, metricsClient *metricsv.Clientset, period time.Duration) (*MetricsData, error) {
	metrics := &MetricsData{
		PodMetrics:  make(map[string]*PodMetrics),
		NodeMetrics: make(map[string]*NodeMetrics),
	}

	// Verificar se o Metrics Server está disponível
	if err := checkMetricsServer(metricsClient); err != nil {
		return nil, err
	}

	interval := 30 * time.Second
	iterations := int(period / interval)

	fmt.Printf("📊 Coletando métricas por %v (intervalo de %v)\n", period, interval)

	for i := 0; i < iterations; i++ {
		fmt.Printf("   Coleta %d/%d...\n", i+1, iterations)

		// Coletar métricas dos pods
		podMetrics, err := metricsClient.MetricsV1beta1().PodMetricses("").List(context.TODO(), metav1.ListOptions{})
		if err != nil {
			fmt.Printf("⚠️  Aviso: Erro ao coletar métricas dos pods: %v\n", err)
			continue
		}

		for _, pod := range podMetrics.Items {
			if _, exists := metrics.PodMetrics[pod.Name]; !exists {
				metrics.PodMetrics[pod.Name] = &PodMetrics{
					Namespace:  pod.Namespace,
					Containers: make(map[string]*ContainerMetrics),
				}
			}

			for _, container := range pod.Containers {
				if _, exists := metrics.PodMetrics[pod.Name].Containers[container.Name]; !exists {
					metrics.PodMetrics[pod.Name].Containers[container.Name] = &ContainerMetrics{}
				}

				// Atualizar máximos
				if container.Usage.Cpu().MilliValue() > metrics.PodMetrics[pod.Name].Containers[container.Name].MaxCPU {
					metrics.PodMetrics[pod.Name].Containers[container.Name].MaxCPU = container.Usage.Cpu().MilliValue()
				}
				if container.Usage.Memory().Value() > metrics.PodMetrics[pod.Name].Containers[container.Name].MaxMemory {
					metrics.PodMetrics[pod.Name].Containers[container.Name].MaxMemory = container.Usage.Memory().Value()
				}
			}
		}

		// Coletar métricas dos nodes
		nodeMetrics, err := metricsClient.MetricsV1beta1().NodeMetricses().List(context.TODO(), metav1.ListOptions{})
		if err != nil {
			fmt.Printf("⚠️  Aviso: Erro ao coletar métricas dos nodes: %v\n", err)
			continue
		}

		for _, node := range nodeMetrics.Items {
			if _, exists := metrics.NodeMetrics[node.Name]; !exists {
				metrics.NodeMetrics[node.Name] = &NodeMetrics{}
			}

			// Atualizar máximos
			if node.Usage.Cpu().MilliValue() > metrics.NodeMetrics[node.Name].MaxCPU {
				metrics.NodeMetrics[node.Name].MaxCPU = node.Usage.Cpu().MilliValue()
			}
			if node.Usage.Memory().Value() > metrics.NodeMetrics[node.Name].MaxMemory {
				metrics.NodeMetrics[node.Name].MaxMemory = node.Usage.Memory().Value()
			}
		}

		time.Sleep(interval)
	}

	return metrics, nil
}

func getDeploymentForPod(clientset *kubernetes.Clientset, pod *corev1.Pod) (string, error) {
	// Verificar se o pod pertence a um deployment
	for _, owner := range pod.OwnerReferences {
		if owner.Kind == "ReplicaSet" {
			// Buscar o ReplicaSet para encontrar o deployment
			rs, err := clientset.AppsV1().ReplicaSets(pod.Namespace).Get(context.TODO(), owner.Name, metav1.GetOptions{})
			if err != nil {
				continue
			}
			for _, rsOwner := range rs.OwnerReferences {
				if rsOwner.Kind == "Deployment" {
					return rsOwner.Name, nil
				}
			}
		}
	}
	return "", nil
}

func aggregateDeploymentMetrics(clientset *kubernetes.Clientset, pods []corev1.Pod, metrics *MetricsData) map[string]*DeploymentMetrics {
	deploymentMetrics := make(map[string]*DeploymentMetrics)

	for _, pod := range pods {
		deploymentName, err := getDeploymentForPod(clientset, &pod)
		if err != nil {
			continue
		}

		// Se não pertence a um deployment, pular
		if deploymentName == "" {
			continue
		}

		key := fmt.Sprintf("%s/%s", pod.Namespace, deploymentName)
		if _, exists := deploymentMetrics[key]; !exists {
			deploymentMetrics[key] = &DeploymentMetrics{
				Name:      deploymentName,
				Namespace: pod.Namespace,
				Pods:      make([]string, 0),
			}
		}

		dm := deploymentMetrics[key]
		dm.Pods = append(dm.Pods, pod.Name)
		dm.TotalPods++

		// Verificar se o pod tem limites definidos
		hasLimits := true
		for _, container := range pod.Spec.Containers {
			if container.Resources.Limits.Cpu().IsZero() || container.Resources.Limits.Memory().IsZero() {
				hasLimits = false
				break
			}
		}
		if !hasLimits {
			dm.PodsWithoutLimits++
		}

		// Agregar métricas do pod
		if podMetrics, exists := metrics.PodMetrics[pod.Name]; exists {
			var totalCPU, totalMemory int64
			for _, containerMetrics := range podMetrics.Containers {
				if containerMetrics.MaxCPU > dm.MaxCPU {
					dm.MaxCPU = containerMetrics.MaxCPU
				}
				if containerMetrics.MaxMemory > dm.MaxMemory {
					dm.MaxMemory = containerMetrics.MaxMemory
				}
				totalCPU += containerMetrics.MaxCPU
				totalMemory += containerMetrics.MaxMemory
			}
			dm.AvgCPU = totalCPU / int64(len(podMetrics.Containers))
			dm.AvgMemory = totalMemory / int64(len(podMetrics.Containers))
		}
	}

	return deploymentMetrics
}

func printUsage() {
	fmt.Println("Uso: k8s-performance-analyzer [opções]")
	fmt.Println("\nOpções:")
	fmt.Println("  -help")
	fmt.Println("        Mostra esta mensagem de ajuda")
	fmt.Println("  -kubeconfig string")
	fmt.Println("        (opcional) Caminho absoluto para o arquivo kubeconfig")
	fmt.Println("  -context string")
	fmt.Println("        (opcional) Nome do contexto do Kubernetes a ser usado")
	fmt.Println("  -periodo string")
	fmt.Println("        (opcional) Período de coleta de métricas (ex: 30m, 1h) (padrão: 5m)")
	fmt.Println("\nExemplos:")
	fmt.Println("  ./k8s-performance-analyzer")
	fmt.Println("  ./k8s-performance-analyzer -context meu-cluster -periodo 30m")
	fmt.Println("  ./k8s-performance-analyzer -kubeconfig /caminho/para/kubeconfig")
}

func main() {
	fmt.Println("🚀 Iniciando análise de performance do Kubernetes...")

	// Definir flags para parâmetros de linha de comando
	var kubeconfig *string
	var k8sContext *string
	var period *string
	var help *bool

	if home := homedir.HomeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(opcional) caminho absoluto para o arquivo kubeconfig")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "caminho absoluto para o arquivo kubeconfig")
	}

	k8sContext = flag.String("context", "", "(opcional) nome do contexto do Kubernetes a ser usado")
	period = flag.String("periodo", "5m", "(opcional) período de coleta de métricas (ex: 30m, 1h)")
	help = flag.Bool("help", false, "mostra a mensagem de ajuda")

	// Configurar o flag.Usage para usar nossa função personalizada
	flag.Usage = printUsage

	flag.Parse()

	// Verificar se a flag help foi usada
	if *help {
		printUsage()
		os.Exit(0)
	}

	// Converter período para duração
	collectionPeriod, err := time.ParseDuration(*period)
	if err != nil {
		fmt.Printf("❌ Erro ao analisar período: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("📋 Configurando conexão com o cluster...\n")
	fmt.Printf("   - Kubeconfig: %s\n", *kubeconfig)
	if *k8sContext != "" {
		fmt.Printf("   - Contexto: %s\n", *k8sContext)
	}
	fmt.Printf("   - Período de coleta: %v\n", collectionPeriod)

	// Configurar o cliente Kubernetes
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: *kubeconfig},
		&clientcmd.ConfigOverrides{CurrentContext: *k8sContext},
	).ClientConfig()

	if err != nil {
		fmt.Printf("❌ Erro ao carregar kubeconfig: %v\n", err)
		os.Exit(1)
	}

	// Obter o contexto atual se não foi especificado
	if *k8sContext == "" {
		rawConfig, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			&clientcmd.ClientConfigLoadingRules{ExplicitPath: *kubeconfig},
			&clientcmd.ConfigOverrides{},
		).RawConfig()
		if err != nil {
			fmt.Printf("❌ Erro ao obter configuração: %v\n", err)
			os.Exit(1)
		}
		*k8sContext = rawConfig.CurrentContext
		fmt.Printf("   - Usando contexto padrão: %s\n", *k8sContext)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		fmt.Printf("❌ Erro ao criar cliente Kubernetes: %v\n", err)
		os.Exit(1)
	}

	// Criar cliente de métricas
	metricsClient, err := metricsv.NewForConfig(config)
	if err != nil {
		fmt.Printf("❌ Erro ao criar cliente de métricas: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("✅ Conexão estabelecida com sucesso!")

	// Criar diretório para relatórios
	reportDir := "performance-reports"
	if err := os.MkdirAll(reportDir, 0755); err != nil {
		fmt.Printf("❌ Erro ao criar diretório de relatórios: %v\n", err)
		os.Exit(1)
	}

	// Gerar nome do arquivo de recomendações com timestamp e contexto sanitizado
	timestamp := time.Now().Format("2006-01-02-15-04-05")
	sanitizedContext := sanitizeFilename(*k8sContext)
	recommendationsFile := filepath.Join(reportDir, fmt.Sprintf("recommendations-%s-%s.txt", sanitizedContext, timestamp))

	// Abrir arquivo de recomendações para escrita
	rec, err := os.Create(recommendationsFile)
	if err != nil {
		fmt.Printf("❌ Erro ao criar arquivo de recomendações: %v\n", err)
		os.Exit(1)
	}
	defer rec.Close()

	// Coletar métricas ao longo do período especificado
	metrics, err := collectMetrics(clientset, metricsClient, collectionPeriod)
	if err != nil {
		fmt.Printf("⚠️  Aviso: %v\n", err)
		fmt.Println("Continuando com a análise sem métricas...")
		metrics = &MetricsData{
			PodMetrics:  make(map[string]*PodMetrics),
			NodeMetrics: make(map[string]*NodeMetrics),
		}
	}

	fmt.Println("\n📊 Analisando recursos do cluster...")

	// Analisar pods
	fmt.Println("   - Listando pods...")
	pods, err := clientset.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		fmt.Printf("❌ Erro ao listar pods: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("   ✅ Encontrados %d pods\n", len(pods.Items))

	// Analisar nodes
	fmt.Println("   - Listando nodes...")
	nodes, err := clientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		fmt.Printf("❌ Erro ao listar nodes: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("   ✅ Encontrados %d nodes\n", len(nodes.Items))

	fmt.Println("\n📝 Gerando recomendações...")

	// Escrever cabeçalho do arquivo de recomendações
	fmt.Fprintf(rec, "Recomendações de Otimização do Kubernetes\n")
	fmt.Fprintf(rec, "Contexto: %s\n", *k8sContext)
	fmt.Fprintf(rec, "Período de análise: %v\n", collectionPeriod)
	fmt.Fprintf(rec, "Gerado em: %s\n\n", time.Now().Format("2006-01-02 15:04:05"))

	// Após coletar as métricas, agregar por deployment
	deploymentMetrics := aggregateDeploymentMetrics(clientset, pods.Items, metrics)

	// Modificar a geração do relatório de recomendações
	fmt.Fprintf(rec, "\n=== Recomendações por Deployment ===\n")
	fmt.Fprintf(rec, "------------------------------------\n")

	for _, dm := range deploymentMetrics {
		fmt.Fprintf(rec, "\nDeployment: %s (Namespace: %s)\n", dm.Name, dm.Namespace)
		fmt.Fprintf(rec, "Total de Pods: %d\n", dm.TotalPods)
		fmt.Fprintf(rec, "Pods sem Limites: %d\n", dm.PodsWithoutLimits)

		if dm.MaxCPU > 0 || dm.MaxMemory > 0 {
			fmt.Fprintf(rec, "\nMétricas (período de %v):\n", collectionPeriod)
			fmt.Fprintf(rec, "  Máximo:\n")
			fmt.Fprintf(rec, "    CPU: %dm\n", dm.MaxCPU)
			fmt.Fprintf(rec, "    Memory: %dMi\n", dm.MaxMemory/1024/1024)
			fmt.Fprintf(rec, "  Média:\n")
			fmt.Fprintf(rec, "    CPU: %dm\n", dm.AvgCPU)
			fmt.Fprintf(rec, "    Memory: %dMi\n", dm.AvgMemory/1024/1024)
		}

		if dm.PodsWithoutLimits > 0 {
			fmt.Fprintf(rec, "\nProblemas Identificados:\n")
			fmt.Fprintf(rec, "1. %d pods sem limites de recursos definidos\n", dm.PodsWithoutLimits)
			fmt.Fprintf(rec, "   Recomendação: Definir limites de recursos (CPU e Memory) para evitar consumo excessivo\n")
			fmt.Fprintf(rec, "   Impacto: Alto - Pode causar problemas de performance no cluster\n")
			fmt.Fprintf(rec, "   Prioridade: Alta\n")
		}

		// Adicionar recomendações baseadas nas métricas
		if dm.MaxCPU > 0 || dm.MaxMemory > 0 {
			fmt.Fprintf(rec, "\nRecomendações de Recursos:\n")
			fmt.Fprintf(rec, "1. Limites sugeridos baseados no uso máximo observado:\n")
			fmt.Fprintf(rec, "   CPU: %dm (máximo observado)\n", dm.MaxCPU)
			fmt.Fprintf(rec, "   Memory: %dMi (máximo observado)\n", dm.MaxMemory/1024/1024)
			fmt.Fprintf(rec, "2. Requests sugeridos baseados na média de uso:\n")
			fmt.Fprintf(rec, "   CPU: %dm (média observada)\n", dm.AvgCPU)
			fmt.Fprintf(rec, "   Memory: %dMi (média observada)\n", dm.AvgMemory/1024/1024)
		}

		fmt.Fprintf(rec, "\nPods Monitorados:\n")
		for _, podName := range dm.Pods {
			fmt.Fprintf(rec, "- %s\n", podName)
		}
		fmt.Fprintf(rec, "\n%s\n", strings.Repeat("-", 80))
	}

	// Adicionar seção de resumo no arquivo de recomendações
	fmt.Fprintf(rec, "\n=== Resumo das Recomendações ===\n")
	fmt.Fprintf(rec, "Total de deployments analisados: %d\n", len(deploymentMetrics))
	fmt.Fprintf(rec, "Total de nodes monitorados: %d\n", len(nodes.Items))

	fmt.Printf("\n✅ Relatório de recomendações gerado com sucesso:\n")
	fmt.Printf("   - Recomendações: %s\n", recommendationsFile)
}
