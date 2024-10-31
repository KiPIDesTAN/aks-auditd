# End-to-end Demo

This end-to-end demo is provided with Terraform. You can run the code locally from your machine with an account that has Contributor at the subscription level.

The demo creates an AKS instance, Log Analytics Workspace, and Data Collection Rule to send the data from AKS to the Log Analytics Workspace. It also deploys the [Container Insights ConfigMap](https://learn.microsoft.com/en-us/azure/azure-monitor/containers/container-insights-data-collection-configure?tabs=portal#configure-data-collection-using-configmap) with a configuration that collects the auditd-aks information from kube-system and send it to the Log Analytics Workspace ContainerInsightsv2 table. See the [container-azm-ms-agentconfig.yaml](./container-azm-ms-agentconfig.yaml) file to see how this was done and compare it to the [default](https://raw.githubusercontent.com/microsoft/Docker-Provider/ci_prod/kubernetes/container-azm-ms-agentconfig.yaml). This is called out because Container Insights does not collect any kube-system namespace logs by default.

__NOTE:__ This code spins up resources. These resources are inexpensive, but not free. Make sure to [destroy](#destroy-the-demo) them when you're done.

### Table of Contents

- [Deploy the Demo](#deploy-the-demo)
- [Review the Demo](#review-the-demo)
- [Destroy the Demo](#destroy-the-demo)

## Deploy the Demo

__NOTE:__ Some versions of the [Kubernetes Terraform Provider](https://registry.terraform.io/providers/hashicorp/kubernetes/latest/docs) throw errors if the AKS cluster doesn't exist before trying to deploy the YAML. If this happens, open the main.tf file, comment out everything from "Get the kubeconfig for the AKS cluster" to the end of the file, run the deployment. Then, uncomment and rerun. You can also get around this issue by having a multi-stage deployment - one Terraform apply creates the AKS instance and a subsequent one deploys the YAML.

Update the [var-demo.auto.tfvars](./terraform/var-demo.auto.tfvars) file with the variables you want to use.

Login to Azure

```console
az login
```

Initialize Terraform

```console
terraform init
```

Run the plan and inspect the results to make sure they're what you want

```console
terraform plan -out plan.tfplan
```

Apply the plan

```console
terraform apply plan.tfplan
```

## Review the Demo

Once the demo is deployed, you'll want to add yourself as a cluster admin to explore how all of this works.

```console
RG_NAME=rg-aks-audit-demo
AKS_NAME=aks-audit-demo
ENTRA_ID=adam_adamnewhard.com#EXT#@adamnewhardgmail.onmicrosoft.com
AKS_ID=$(az aks show -g $RG_NAME -n $AKS_NAME --query id -o tsv)
az role assignment create --role "Azure Kubernetes Service RBAC Cluster Admin" --assignee $ENTRA_ID --scope $AKS_ID
```

Install kubectl and kubelogin if you don't already have it

```console
sudo az aks install-cli
```

Login to the AKS cluster

```console
az aks get-credentials -g $RG_NAME -n $AKS_NAME
```

Run the command below to view the deployed ConfigMaps which support auditd rules and audisp-plugins.

```console
kubectl describe ConfigMap -n kube-system -l name=aks-auditd
```

Run the command to see the deployed Daemonset Pods.

```console
kubectl get pod -n kube-system -l name=aks-auditd
```

Use one of the Pod names returned to get the logs.

```console
kubectl logs aks-auditd-jckql -n kube-system
```

To see the information on the cluster, connect to one of the nodes you're interested in.

```console
> kubectl get nodes -o wide
NAME                              STATUS   ROLES    AGE   VERSION   INTERNAL-IP   EXTERNAL-IP   OS-IMAGE             KERNEL-VERSION      CONTAINER-RUNTIME
<node_name>   Ready    <none>   50m   v1.29.7   x.x.x.x       <none>        Ubuntu 22.04.4 LTS   5.15.0-1071-azure   containerd://1.7.20-1
```

Connect to the node via busybox.

```console
kubectl debug node/<node_name> -it --image=mcr.microsoft.com/cbl-mariner/busybox:2.0
```

The node is located at /host. If you need to interact with the host directly, you can do so with the command below.

```console
chroot /host
```

The commands below assume you have not run chroot, so they include "/host" on the directory paths.

To see the audit rules that were applied via the ConfigMap after the aks-auditd binary copied them to the host, run

```console
ls /host/etc/audit/rules.d/
```

You can output any of these to the terminal.

When auditd restarts, it recreates an audit.rules file based on those that were stored in the rules.d directory. You can see the generated rules that are currently loaded via the command below.

```console
cat /host/etc/audit/audit.rules
```

To see the audisp-plugins configuration for syslog, run

```console
cat /host/etc/audit/plugins.d/syslog.conf
```

To see the status of the auditd service, do so via chroot. When done, run "exit" to leave the chroot environment.

```console
chroot /host
```

Get the service status

```console
systemctl status auditd
```

Get the auditd service logs

```console
journalctl -u auditd.service
```

Leave the chroot
```console
exit
```

By default, the auditd logs are collected in the Syslog user facility. You can see those with the Log Analytics Workspace query below.

```kusto
Syslog
| where Facility == 'user'
```

If your user facility collects other information, you can run the query below to identify Syslog values created with the audisp-syslog process on the node or send the auditd information to another facility.

```kusto
Syslog
| where Facility == 'user' and ProcessName == 'audisp-syslog'
```



## Destroy the Demo

Make sure to destroy the resources when you're done.

```console
terraform destroy
```
