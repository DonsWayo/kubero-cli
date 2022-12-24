package cmd

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/exec"
	"time"

	"encoding/json"

	"github.com/go-resty/resty/v2"
	"github.com/i582/cfmt/cmd/cfmt"
	"github.com/leaanthony/spinner"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
	"k8s.io/client-go/tools/clientcmd"
)

// installCmd represents the install command
var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install all required components for kubero",
	Long: `This command will try to install all required components for kubero on a kubernetes cluster.
It is allso possible to install a local kind cluster.

required binaries:
 - kubectl
 - kind (optional)`,
	Run: func(cmd *cobra.Command, args []string) {

		rand.Seed(time.Now().UnixNano())

		checkAllBinaries()

		switch arg_component {
		case "metrics":
			installMetrics()
			return
		case "certmanager":
			installCertManager()
			return
		case "olm":
			installOLM()
			return
		case "kubero":
			installKuberoOperator()
			installKuberoUi()
			return
		case "ingress":
			installIngress()
			return
		case "kubernetes":
			installKubernetes()
			checkCluster()
			return
		default:
			installKubernetes()
			checkCluster()
			installOLM()
			installIngress()
			installMetrics()
			installCertManager()
			installKuberoOperator()
			installKuberoUi()
			writeCLIconfig()
			printDNSinfo()
			finalMessage()
			return
		}
	},
}

var arg_adminPassword string
var arg_adminUser string
var arg_domain string
var arg_apiToken string
var arg_port string
var arg_portSecure string
var clusterType string
var arg_component string
var ingressControllerVersion = "v1.5.1" // https://github.com/kubernetes/ingress-nginx/tags -> controller-v1.5.1

var clusterTypeSelection = "[scaleway,linode,gke,digitalocean,kind]"

func init() {
	installCmd.Flags().StringVarP(&arg_component, "component", "c", "", "install sincel component (kubernetes,olm,ingress,metrics,certmanager,kubero-operator,kubero-ui)")
	installCmd.Flags().StringVarP(&arg_adminUser, "user", "u", "", "Admin username for the kubero UI")
	installCmd.Flags().StringVarP(&arg_adminPassword, "user-password", "U", "", "Password for the admin user")
	installCmd.Flags().StringVarP(&arg_apiToken, "apitoken", "a", "", "API token for the admin user")
	installCmd.Flags().StringVarP(&arg_port, "port", "p", "", "Kubero UI HTTP port")
	installCmd.Flags().StringVarP(&arg_portSecure, "secureport", "P", "", "Kubero UI HTTPS port")
	installCmd.Flags().StringVarP(&arg_domain, "domain", "d", "", "Domain name for the kubero UI")
	rootCmd.AddCommand(installCmd)
}

func checkAllBinaries() {
	cfmt.Println("{{\n  Check for required binaries}}::lightWhite")
	if !checkBinary("kubectl") {
		cfmt.Println("{{✗ kubectl is not installed}}::red")
	} else {
		cfmt.Println("{{✓ kubectl is installed}}::lightGreen")
	}

	if !checkBinary("kind") {
		cfmt.Println("{{⚠ kind is not installed}}::yellow (only required if you want to install a local kind cluster)")
	} else {
		cfmt.Println("{{✓ kind is installed}}::lightGreen")
	}

	if !checkBinary("gcloud") {
		cfmt.Println("{{⚠ gcloud is not installed}}::yellow (only required if you want to install a GKE cluster)")
	} else {
		cfmt.Println("{{✓ gcloud is installed}}::lightGreen")
	}
}

func checkBinary(binary string) bool {
	_, err := exec.LookPath(binary)
	return err == nil
}

func installKubernetes() {
	kubernetesInstall := promptLine("Start a kubernetes Cluster", "[y,n]", "y")
	if kubernetesInstall != "y" {
		return
	}

	clusterType = promptLine("Select a cluster type", clusterTypeSelection, "linode")

	switch clusterType {
	case "scaleway":
		installScaleway()
	case "linode":
		installLinode()
	case "gke":
		installGKE()
	case "digitalocean":
		installDigitalOcean()
	case "kind":
		installKind()
	default:
		cfmt.Println("{{✗ Unknown cluster type}}::red")
		os.Exit(1)
	}

}

func tellAChucknorrisJoke() {

	jokesapi := resty.New().
		SetHeader("Accept", "application/json").
		SetHeader("Content-Type", "application/json").
		SetHeader("User-Agent", "kubero-cli/0.0.1").
		SetBaseURL("https://api.chucknorris.io/jokes/random")

	joke, _ := jokesapi.R().Get("?category=dev")
	var jokeResponse JokeResponse
	json.Unmarshal(joke.Body(), &jokeResponse)
	cfmt.Println("\r{{  " + jokeResponse.Value + "       }}::gray")
}

func mergeKubeconfig(kubeconfig []byte) error {

	new := clientcmd.NewDefaultPathOptions()
	config1, _ := new.GetStartingConfig()
	config2, err := clientcmd.Load(kubeconfig)
	if err != nil {
		return err
	}
	// append the second config to the first
	for k, v := range config2.Clusters {
		config1.Clusters[k] = v
	}
	for k, v := range config2.AuthInfos {
		config1.AuthInfos[k] = v
	}
	for k, v := range config2.Contexts {
		config1.Contexts[k] = v
	}

	config1.CurrentContext = config2.CurrentContext

	clientcmd.ModifyConfig(clientcmd.DefaultClientConfig.ConfigAccess(), *config1, true)
	return nil
}

func checkCluster() {
	var outb, errb bytes.Buffer

	clusterInfo := exec.Command("kubectl", "cluster-info")
	clusterInfo.Stdout = &outb
	clusterInfo.Stderr = &errb
	err := clusterInfo.Run()
	if err != nil {
		fmt.Println(errb.String())
		fmt.Println(outb.String())
		log.Fatal("command failed : kubectl cluster-info")
	}
	fmt.Println(outb.String())

	out, _ := exec.Command("kubectl", "config", "get-contexts").Output()
	fmt.Println(string(out))

	clusterselect := promptLine("Is the CURRENT cluster the one you wish to install Kubero?", "[y,n]", "y")
	if clusterselect == "n" {
		os.Exit(0)
	}
}

func installOLM() {

	openshiftInstalled, _ := exec.Command("kubectl", "get", "deployment", "olm-operator", "-n", "openshift-operator-lifecycle-manager").Output()
	if len(openshiftInstalled) > 0 {
		cfmt.Println("{{✓ OLM is allredy installed}}::lightGreen")
		return
	}

	//namespace := promptLine("Install OLM in which namespace?", "[openshift-operator-lifecycle-manager,olm]", "olm")
	namespace := "olm"
	olmInstalled, _ := exec.Command("kubectl", "get", "deployment", "olm-operator", "-n", namespace).Output()
	if len(olmInstalled) > 0 {
		cfmt.Println("{{✓ OLM is allredy installed}}::lightGreen")
		return
	}

	olmInstall := promptLine("Install OLM", "[y,n]", "y")
	if olmInstall != "y" {
		log.Fatal("OLM is required to install Kubero")
	}

	olmRelease := promptLine("Install OLM from which release?", "[0.19.0,0.20.0,0.21.0,0.22.0]", "0.22.0")
	olmURL := "https://github.com/operator-framework/operator-lifecycle-manager/releases/download/v" + olmRelease

	olmSpinner := spinner.New("Install OLM")

	olmCRDInstalled, _ := exec.Command("kubectl", "get", "crd", "subscriptions.operators.coreos.com").Output()
	if len(olmCRDInstalled) > 0 {
		cfmt.Println("{{✓ OLM CRD's allredy installed}}::lightGreen")
	} else {
		olmSpinner.Start("run command : kubectl create -f " + olmURL + "/olm.yaml")
		_, olmCRDErr := exec.Command("kubectl", "create", "-f", olmURL+"/crds.yaml").Output()
		if olmCRDErr != nil {
			fmt.Println("")
			olmSpinner.Error("OLM CRD installation failed. Try runnig it manually")
			log.Fatal(olmCRDErr)
		} else {
			olmSpinner.Success("OLM CRDs installed sucessfully")
		}
	}

	olmSpinner.Start("run command : kubectl create -f " + olmURL + "/olm.yaml")

	_, olmOLMErr := exec.Command("kubectl", "create", "-f", olmURL+"/olm.yaml").Output()
	if olmOLMErr != nil {
		fmt.Println("")
		olmSpinner.Error("Failed to run command. Try runnig it manually")
		log.Fatal(olmOLMErr)
	}
	olmSpinner.Success("OLM installed sucessfully")

	olmWaitSpinner := spinner.New("Wait for OLM to be ready")
	olmWaitSpinner.Start("run command : kubectl wait --for=condition=available deployment/olm-operator -n " + namespace + " --timeout=180s")
	_, olmWaitErr := exec.Command("kubectl", "wait", "--for=condition=available", "deployment/olm-operator", "-n", namespace, "--timeout=180s").Output()
	if olmWaitErr != nil {
		olmWaitSpinner.Error("Failed to run command. Try runnig it manually")
		log.Fatal(olmWaitErr)
	}
	olmWaitSpinner.Success("OLM is ready")

	olmWaitCatalogSpinner := spinner.New("Wait for OLM Catalog to be ready")
	olmWaitCatalogSpinner.Start("run command : kubectl wait --for=condition=available deployment/catalog-operator -n " + namespace + " --timeout=180s")
	_, olmWaitCatalogErr := exec.Command("kubectl", "wait", "--for=condition=available", "deployment/catalog-operator", "-n", namespace, "--timeout=180s").Output()
	if olmWaitCatalogErr != nil {
		olmWaitCatalogSpinner.Error("Failed to run command. Try runnig it manually")
		log.Fatal(olmWaitCatalogErr)
	}
	olmWaitCatalogSpinner.Success("OLM Catalog is ready")
}

func installMetrics() {

	ingressInstalled, _ := exec.Command("kubectl", "get", "deployments.apps", "metrics-serverXXXX", "-n", "kube-system").Output()
	if len(ingressInstalled) > 0 {
		cfmt.Println("{{✓ Metrics is allredy enabled}}::lightGreen")
		return
	}
	ingressInstall := promptLine("Install Kubernetes internal metrics service (ruquired for HPA and stats)", "[y,n]", "y")
	if ingressInstall != "y" {
		return
	}
	/*
	   _, installErr := exec.Command("kubectl", "apply", "-f", "https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml").Output()

	   	if installErr != nil {
	   		fmt.Println("failed to install metrics server")
	   		log.Fatal(installErr)
	   	}

	   asdf := "-p='[{\"op\": \"add\", \"path\": \"/spec/template/spec/containers/0/args/1\", \"value\": \"--kubelet-insecure-tls\"}]'"
	   fmt.Println(asdf)
	   //ret, patchErr := exec.Command("kubectl", "-v=8", "patch", "deployment", "metrics-server", "-n", "kube-system", "--type=json", `-p='[{"op": "add", "path": "/spec/template/spec/containers/0/args/1", "value": "--kubelet-insecure-tls"}]'`).CombinedOutput()
	   ret, patchErr := exec.Command("kubectl", "-v=8", "patch", "deployment", "metrics-server", "-n", "kube-system", "--type=json", `-p='[{op: add, path: /spec/template/spec/containers/0/args/1, value: --kubelet-insecure-tls}]'`).CombinedOutput()
	   //ret, patchErr := exec.Command("kubectl", "patch", "deployment", "metrics-server", "-n", "kube-system", "--type=json", "-p='[{\\\"op\\\": \\\"add\\\", \\\"path\\\": \\\"/spec/template/spec/containers/0/args/1\\\", \\\"value\\\": \\\"--kubelet-insecure-tls\\\"}]'").CombinedOutput()
	   //ret, patchErr := exec.Command("kubectl", "patch", "deployment", "metrics-server", "-n", "kube-system", "--type=json", asdf).CombinedOutput()
	   //ret, patchErr := exec.Command("kubectl", "-v=8", "patch", "deployment", "metrics-server", "-n", "kube-system", "--type=json", "-p=\"[{'op': 'add', 'path': '/spec/template/spec/containers/0/args/1', 'value': '--kubelet-insecure-tls'}]\"").CombinedOutput()

	   	if patchErr != nil {
	   		fmt.Println("failed to patch metrics server")
	   		fmt.Println(string(ret))
	   		log.Fatal(patchErr)
	   	}
	*/
}

func installIngress() {

	ingressInstalled, _ := exec.Command("kubectl", "get", "ns", "ingress-nginx").Output()
	if len(ingressInstalled) > 0 {
		cfmt.Println("{{✓ Ingress is allredy installed}}::lightGreen")
		return
	}

	ingressInstall := promptLine("Install Ingress", "[y,n]", "y")
	if ingressInstall != "y" {
		return
	} else {

		if clusterType == "" {
			clusterType = promptLine("Which cluster type have you insalled?", clusterTypeSelection, "")
		}

		prefill := "baremetal"
		switch clusterType {
		case "kind":
			prefill = "kind"
		case "linode":
			prefill = "cloud"
		case "gke":
			prefill = "cloud"
		case "scaleway":
			prefill = "scw"
		case "digitalocean":
			prefill = "do"
		}

		ingressProvider := promptLine("Provider", "[kind,aws,baremetal,cloud(Azure,Google,Oracle,Linode),do(digital ocean),exoscale,scw(scaleway)]", prefill)
		ingressSpinner := spinner.New("Install Ingress")
		URL := "https://raw.githubusercontent.com/kubernetes/ingress-nginx/controller-" + ingressControllerVersion + "/deploy/static/provider/" + ingressProvider + "/deploy.yaml"
		ingressSpinner.Start("run command : kubectl apply -f " + URL)
		_, ingressErr := exec.Command("kubectl", "apply", "-f", URL).Output()
		if ingressErr != nil {
			ingressSpinner.Error("Failed to run command. Try runnig it manually")
			log.Fatal(ingressErr)
		}
		ingressSpinner.Success("Ingress installed sucessfully")
	}

}

func installKuberoOperator() {

	cfmt.Println("{{\n  Install Kubero Operator}}::lightWhite")

	kuberoInstalled, _ := exec.Command("kubectl", "get", "operator", "kubero-operator.operators").Output()
	if len(kuberoInstalled) > 0 {
		cfmt.Println("{{✓ Kubero Operator is allredy installed}}::lightGreen")
		return
	}

	kuberoSpinner := spinner.New("Install Kubero Operator")
	kuberoSpinner.Start("run command : kubectl apply -f https://operatorhub.io/install/kubero-operator.yaml")
	_, kuberoErr := exec.Command("kubectl", "apply", "-f", "https://operatorhub.io/install/kubero-operator.yaml").Output()
	if kuberoErr != nil {
		fmt.Println("")
		kuberoSpinner.Error("Failed to run command to install the Operator. Try runnig it manually and then rerun the installation")
		log.Fatal(kuberoErr)
	}

	kuberoSpinner.UpdateMessage("Wait for Kubero Operator to be ready")
	var kuberoWait []byte
	for len(kuberoWait) == 0 {
		// kubectl api-resources --api-group=application.kubero.dev --no-headers=true
		kuberoWait, _ = exec.Command("kubectl", "api-resources", "--api-group=application.kubero.dev", "--no-headers=true").Output()
		time.Sleep(1 * time.Second)
	}

	kuberoSpinner.Success("Kubero Operator installed sucessfully")

}

func installKuberoUi() {

	ingressInstall := promptLine("Install Kubero UI", "[y,n]", "y")
	if ingressInstall != "y" {
		return
	}

	kuberoNSinstalled, _ := exec.Command("kubectl", "get", "ns", "kubero").Output()
	if len(kuberoNSinstalled) > 0 {
		cfmt.Println("{{✓ Kubero Namespace exists}}::lightGreen")
	} else {
		_, kuberoNSErr := exec.Command("kubectl", "create", "namespace", "kubero").Output()
		if kuberoNSErr != nil {
			fmt.Println("Failed to run command to create the namespace. Try runnig it manually")
			log.Fatal(kuberoNSErr)
		} else {
			cfmt.Println("{{✓ Kubero Namespace created}}::lightGreen")
		}
	}

	kuberoSecretInstalled, _ := exec.Command("kubectl", "get", "secret", "kubero-secrets", "-n", "kubero").Output()
	if len(kuberoSecretInstalled) > 0 {
		cfmt.Println("{{✓ Kubero Secret exists}}::lightGreen")
	} else {

		webhookSecret := promptLine("Random string for your webhook secret", "", generatePassword(20))

		sessionKey := promptLine("Random string for your session key", "", generatePassword(20))

		if arg_adminUser == "" {
			arg_adminUser = promptLine("Admin User", "", "admin")
		}

		if arg_adminPassword == "" {
			arg_adminPassword = promptLine("Admin Password", "", generatePassword(12))
		}

		if arg_apiToken == "" {
			arg_apiToken = promptLine("Random string for admin API token", "", generatePassword(20))
		}

		var userDB []User
		userDB = append(userDB, User{Username: arg_adminUser, Password: arg_adminPassword, Insecure: true, Apitoken: arg_apiToken})
		userDBjson, _ := json.Marshal(userDB)
		userDBencoded := base64.StdEncoding.EncodeToString(userDBjson)

		createSecretCommand := exec.Command("kubectl", "create", "secret", "generic", "kubero-secrets",
			"--from-literal=KUBERO_WEBHOOK_SECRET="+webhookSecret,
			"--from-literal=KUBERO_SESSION_KEY="+sessionKey,
			"--from-literal=KUBERO_USERS="+userDBencoded,
		)

		githubConfigure := promptLine("Configure Github", "[y,n]", "y")
		githubPersonalAccessToken := ""
		if githubConfigure == "y" {
			githubPersonalAccessToken = promptLine("Github personal access token", "", "")
			createSecretCommand.Args = append(createSecretCommand.Args, "--from-literal=GITHUB_PERSONAL_ACCESS_TOKEN="+githubPersonalAccessToken)
		}

		giteaConfigure := promptLine("Configure Gitea", "[y,n]", "n")
		giteaPersonalAccessToken := ""
		giteaBaseUrl := ""
		if giteaConfigure == "y" {
			giteaPersonalAccessToken = promptLine("Gitea personal access token", "", "")
			giteaBaseUrl = promptLine("Gitea URL", "http://localhost:3000", "")
			createSecretCommand.Args = append(createSecretCommand.Args, "--from-literal=GITEA_PERSONAL_ACCESS_TOKEN="+giteaPersonalAccessToken)
			createSecretCommand.Args = append(createSecretCommand.Args, "--from-literal=GITEA_BASEURL="+giteaBaseUrl)
		}

		gogsConfigure := promptLine("Configure Gogs", "[y,n]", "n")
		gogsPersonalAccessToken := ""
		gogsBaseUrl := ""
		if gogsConfigure == "y" {
			gogsPersonalAccessToken = promptLine("Gogs personal access token", "", "")
			gogsBaseUrl = promptLine("Gogs URL", "http://localhost:3000", "")
			createSecretCommand.Args = append(createSecretCommand.Args, "--from-literal=GOGS_PERSONAL_ACCESS_TOKEN="+gogsPersonalAccessToken)
			createSecretCommand.Args = append(createSecretCommand.Args, "--from-literal=GOGS_BASEURL="+gogsBaseUrl)
		}

		gitlabConfigure := promptLine("Configure Gitlab", "[y,n]", "n")
		gitlabPersonalAccessToken := ""
		gitlabBaseUrl := ""
		if gitlabConfigure == "y" {
			gitlabPersonalAccessToken = promptLine("Gitlab personal access token", "", "")
			gitlabBaseUrl = promptLine("Gitlab URL", "http://localhost:3080", "")
			createSecretCommand.Args = append(createSecretCommand.Args, "--from-literal=GITLAB_PERSONAL_ACCESS_TOKEN="+gitlabPersonalAccessToken)
			createSecretCommand.Args = append(createSecretCommand.Args, "--from-literal=GITLAB_BASEURL="+gitlabBaseUrl)
		}

		bitbucketConfigure := promptLine("Configure Bitbucket", "[y,n]", "n")
		bitbucketUsername := ""
		bitbucketAppPassword := ""
		if bitbucketConfigure == "y" {
			bitbucketUsername = promptLine("Bitbucket Username", "", "")
			bitbucketAppPassword = promptLine("Bitbucket App Password", "", "")
			createSecretCommand.Args = append(createSecretCommand.Args, "--from-literal=BITBUCKET_USERNAME="+bitbucketUsername)
			createSecretCommand.Args = append(createSecretCommand.Args, "--from-literal=BITBUCKET_APP_PASSWORD="+bitbucketAppPassword)
		}

		createSecretCommand.Args = append(createSecretCommand.Args, "-n", "kubero")

		_, kuberoErr := createSecretCommand.Output()

		if kuberoErr != nil {
			cfmt.Println("{{✗ Failed to run command to create the secret. Try runnig it manually}}::red")
			log.Fatal(kuberoErr)
		} else {
			cfmt.Println("{{✓ Kubero Secret created}}::lightGreen")
		}
	}

	kuberoUIInstalled, _ := exec.Command("kubectl", "get", "kuberoes.application.kubero.dev", "-n", "kubero").Output()
	if len(kuberoUIInstalled) > 0 {
		cfmt.Println("{{✓ Kubero UI allready installed}}::lightGreen")
	} else {
		installer := resty.New()

		installer.SetBaseURL("https://raw.githubusercontent.com")
		kf, _ := installer.R().Get("kubero-dev/kubero-operator/main/config/samples/application_v1alpha1_kubero.yaml")

		var kuberiUIConfig KuberoUIConfig
		yaml.Unmarshal(kf.Body(), &kuberiUIConfig)

		if arg_domain == "" {
			arg_domain = promptLine("Kuberi UI Domain", "", "kubero.lacolhost.com")
		}
		kuberiUIConfig.Spec.Ingress.Hosts[0].Host = arg_domain

		webhookURL := promptLine("URL to which the webhooks should be sent", "", arg_domain+"/api/repo/webhooks")
		kuberiUIConfig.Spec.Kubero.WebhookURL = webhookURL

		if clusterType == "" {
			clusterType = promptLine("Which cluster type have you insalled?", clusterTypeSelection, "")
		}

		if clusterType == "linode" ||
			clusterType == "digitalocean" ||
			clusterType == "gke" {
			kuberiUIConfig.Spec.Ingress.ClassName = "nginx"
		}

		kuberiUIYaml, _ := yaml.Marshal(kuberiUIConfig)
		kuberiUIErr := os.WriteFile("kuberoUI.yaml", kuberiUIYaml, 0644)
		if kuberiUIErr != nil {
			fmt.Println(kuberiUIErr)
			return
		}

		_, olminstallErr := exec.Command("kubectl", "apply", "-f", "kuberoUI.yaml", "-n", "kubero").Output()
		if olminstallErr != nil {
			fmt.Println(olminstallErr)
			cfmt.Println("{{✗ Failed to run command to install Kubero UI. Rerun installer to finish installation}}::red")
			return
		} else {
			e := os.Remove("kuberoUI.yaml")
			if e != nil {
				log.Fatal(e)
			}
			cfmt.Println("{{✓ Kubero UI installed}}::lightGreen")
		}

		kuberoUISpinner := spinner.New("Wait for Kubero UI to be ready")
		time.Sleep(8 * time.Second) //linide needs a bit more time to get the ingress up
		kuberoUISpinner.Start("run command : kubectl wait --for=condition=available deployment/kubero-sample -n kubero --timeout=180s")
		_, olmWaitErr := exec.Command("kubectl", "wait", "--for=condition=available", "deployment/kubero-sample", "-n", "kubero", "--timeout=180s").Output()
		if olmWaitErr != nil {
			fmt.Println("") // keeps the spinner from overwriting the last line
			kuberoUISpinner.Error("Failed to run command. Rerun installer to finish installation")
			log.Fatal(olmWaitErr)
		}
		kuberoUISpinner.Success("Kubero UI is ready")
	}

}

func installCertManager() {
	certManagerInstalled, _ := exec.Command("kubectl", "get", "deployment", "cert-manager-webhook", "-n", "operators").Output()
	if len(certManagerInstalled) > 0 {
		cfmt.Println("{{✓ Cert Manager allready installed}}::lightGreen")
	} else {

		install := promptLine("Install SSL Certmanager", "[y,n]", "y")
		if install != "y" {
			return
		}

		certManagerSpinner := spinner.New("Install Cert Manager")
		certManagerSpinner.Start("run command : kubectl create -f https://operatorhub.io/install/cert-manager.yaml")
		_, certManagerErr := exec.Command("kubectl", "create", "-f", "https://operatorhub.io/install/cert-manager.yaml").Output()
		if certManagerErr != nil {
			fmt.Println("") // keeps the spinner from overwriting the last line
			certManagerSpinner.Error("Failed to run command. Try runnig it manually")
			log.Fatal(certManagerErr)
		}
		certManagerSpinner.Success("Cert Manager installed")

		time.Sleep(2 * time.Second)
		certManagerSpinner = spinner.New("Wait for Cert Manager to be ready")
		certManagerSpinner.Start("run command : kubectl wait --for=condition=available deployment/cert-manager-webhook -n cert-manager --timeout=180s -n operators")
		_, certManagerWaitErr := exec.Command("kubectl", "wait", "--for=condition=available", "deployment/cert-manager-webhook", "-n", "cert-manager", "--timeout=180s", "-n", "operators").Output()
		if certManagerWaitErr != nil {
			fmt.Println("") // keeps the spinner from overwriting the last line
			certManagerSpinner.Error("Failed to run command. Try runnig it manually")
			log.Fatal(certManagerWaitErr)
		}
		certManagerSpinner.Success("Cert Manager is ready")
	}
}

func writeCLIconfig() {

	ingressInstall := promptLine("Generate CLI config", "[y,n]", "y")
	if ingressInstall != "y" {
		return
	}

	//TODO consider using SSL here.
	url := promptLine("Kubero Host adress", "", "http://"+arg_domain+":"+arg_port)
	viper.Set("api.url", url)

	token := promptLine("Kubero Token", "", arg_apiToken)
	viper.Set("api.token", token)

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Printf("%+v\n", config)

	viper.WriteConfig()
}

func printDNSinfo() {

	ingressInstalled, err := exec.Command("kubectl", "get", "ingress", "-n", "kubero", "-o", "json").Output()
	if err != nil {
		cfmt.Println("{{✗ Failed to fetch DNS informations}}::red")
		return
	}
	var kuberoIngress KuberoIngress
	json.Unmarshal(ingressInstalled, &kuberoIngress)

	cfmt.Println("{{⚠ make sure your DNS is pointing to your Kubernetes cluster}}::yellow")

	//TODO this should be replaces by the default reviewapp domain
	if len(kuberoIngress.Items) > 0 &&
		len(kuberoIngress.Items[0].Spec.Rules[0].Host) > 0 &&
		len(kuberoIngress.Items[0].Status.LoadBalancer.Ingress) > 0 &&
		len(kuberoIngress.Items[0].Status.LoadBalancer.Ingress[0].IP) > 0 {
		cfmt.Printf("{{  %s.		IN		A		%s}}::lightBlue\n", kuberoIngress.Items[0].Spec.Rules[0].Host, kuberoIngress.Items[0].Status.LoadBalancer.Ingress[0].IP)
		cfmt.Printf("{{  *.review.example.com.			IN		A		%s}}::lightBlue", kuberoIngress.Items[0].Status.LoadBalancer.Ingress[0].IP)
	}

}

func finalMessage() {
	cfmt.Println(`

    ,--. ,--.        ,--.
    |  .'   /,--.,--.|  |-.  ,---. ,--.--. ,---.
    |  .   ' |  ||  || .-. '| .-. :|  .--'| .-. |
    |  |\   \'  ''  '| '-' |\   --.|  |   ' '-' '
    '--' '--' '----'  '---'  '----''--'    '---'

    Documentation:
    https://github.com/kubero-dev/kubero/wiki
`)

	if arg_domain != "" && arg_port != "" && arg_apiToken != "" && arg_adminPassword != "" {
		cfmt.Println(`
    Your Kubero UI :{{
    URL : ` + arg_domain + `:` + arg_port + `
    User: ` + arg_adminUser + `
    Pass: ` + arg_adminPassword + `}}::lightBlue
	`)
	} else {
		cfmt.Println("\n\n    {{Done - you can now login to your Kubero UI}}::lightGreen\n\n")
	}
}

func generatePassword(length int) string {
	var letterRunes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!+?._-%")
	b := make([]rune, length)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}
