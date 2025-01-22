# Overview

__NOTE:__ This repository was originally forked from the [Azure aks-auditd](https://github.com/Azure/aks-auditd) implementation utilizing the OMS legacy agent. However, this implementation deviates significantly from the original and should be considered a solution unto itself. See [here](#what-changes-occurred-between-the-original-azure-implementation-and-this-one) for differences.

Linux auditd is a userspace component responsible for monitoring and logging system calls and events for security auditing purposes. It tracks file access, user activity, and changes to system configurations, providing detailed logs to help administrators monitor and review actions on the system. auditd is often used to ensure compliance with security policies and to investigate suspicious behavior or breaches. 

aks-auditd is an implementation of auditd for [Azure Kuberentes Service](https://learn.microsoft.com/en-us/azure/aks/what-is-aks) worker nodes. It runs as a DaemonSet to deploy auditd, audispd-plugins, and a utility application to the node whenever a new node is created in a pool. The auditd service collects audit information and sends it to the node's syslog implementation. AKS relies on the the deployment of the [Container Insights add-on](https://learn.microsoft.com/en-us/azure/azure-monitor/containers/kubernetes-monitoring-enable?tabs=cli#enable-container-insights) within the cluster and a Container Insights data collection rule configured to [collect Syslog data](https://learn.microsoft.com/en-us/azure/azure-monitor/containers/container-insights-syslog). Once collected, the data arrives in the Log Analytics Workspace's Syslog table.

*A deployable end-to-end demo is available* in the [demo](./demo/README.md) folder. This should be the first place you look to understand how the solution works.

## Pre-built Container Image

You can pull the latest container(s) with the command below or check-out the [aks-auditd packages](https://github.com/KiPIDesTAN/aks-auditd/pkgs/container/aks-auditd) for a specific version.

```console
docker pull ghcr.io/kipidestan/aks-auditd:latest
docker pull ghcr.io/kipidestan/aks-auditd-init:latest
```

Please, note using the latest tag should only be done for testing. Updates to the aks-aduitd images are not guaranteed to work with old deployment yaml. Securing a container that runs with elevated privileges is a moving target. Thus, you should use the version tag of a specific container when deploying to production.

If you need to review changes for a specific container, look at the git tag associated with the container version.

### Image Verification

Published images are signed with [cosign](https://github.com/sigstore/cosign) keyless signing and a Software Bill of Materials (SBOM).

#### Cosign Verification

Use the command below to verify the image signature. You will want to verify each image.

NOTE: The value of certificate identity is case-sensitive. If "KiPIDesTAN" is not written correctly, the verification will fail.

```console
cosign verify ghcr.io/kipidestan/aks-auditd:latest \
  --certificate-identity="https://github.com/KiPIDesTAN/aks-auditd/.github/workflows/scan-publish.yml@refs/heads/main" \
  --certificate-oidc-issuer="https://token.actions.githubusercontent.com" | jq
```

#### Software Bill of Materials

The Software Bill of Materials (SBOM) requires the [Docker SBOM cli plugin](https://github.com/docker/sbom-cli-plugin). Once installed, pull the image and run the command below:

```console
docker pull aks-auditd:latest
docker sbom aks-auditd:latest
```

NOTE: There are two images involved in this deployment. You will want to check both images.

## Build and Deploy

This repo comes with a GitHub action to build and push the aks-auditd containers. Please, review it and use the information below to enhance your understanding of the build process.

The build process utilizes a multi-stage build, pulling the Azure Linux core image, compiling the Go binary, and copying the required artifacts to the Azure Linux Distroless minimal image of the same version. View Dockerfiles in the root directory of the project to understand the implementation.

The commands below assume you have an Azure Container Registry accessible to push images to. The ACR must be accessible by your AKS cluster in order to deploy the image.

Set the appropriate variables for the build process.

```console
RG_NAME=<resource_group_name>
ACR_NAME=<acr_name>
ACR_URL=$(echo "$ACR_NAME.azurecr.io")
IMAGE_NAME_INIT=aks-auditd-init
IMAGE_NAME_RUN=aks-auditd-run
IMAGE_TAG=<image_tag>
```

Login to your ACR

```console
az login
az acr login --name $ACR_NAME --resource-group $RG_NAME
```

Build the image locally and push it to the ACR.

```console
docker buildx build -f Dockerfile.init -t $ACR_URL/$IMAGE_NAME_INIT:$IMAGE_TAG .
docker buildx build -f Dockerfile.run -t $ACR_URL/$IMAGE_NAME_RUN:$IMAGE_TAG .
```


To push the images to your ACR, run

```console
docker push $ACR_URL/$IMAGE_NAME_INIT:$IMAGE_TAG
docker push $ACR_URL/$IMAGE_NAME_RUN:$IMAGE_TAG
```

Once built, you can deploy the image. See an example of the deployment file at <project_root>/kubernetes/daemonset.yaml. 

## Configuration

The following values can be configured via environment variable or ConfigMap. The order of precedence is the following:

1. Environment variable
2. ConfigMap value
3. Default value

| Item |  Environment Variable | Config File Value | Default | Notes |
|---|---|---|---|--|
| Log Level |  AA_LOG_LEVEL | logLevel | 'info' | Valid values: panic, fatal, error, warn, info, debug, trace |

### Configuration via ConfigMap

An example of the config.yaml ConfigMap to configure the Go binary is below or [here](./config.yaml). Once you've created your own ConfigMap, you will want to apply it on the container to "/etc/aks-auditd/config.yaml" as part of your [daemonset.yaml](./kubernetes/daemonset.yaml) deployment.

## Golang Code Style

The code managing the deployment and execution is fundamentally a series of shell and kernel commands, but written in [Go](https://go.dev/). The code is written sequentially, like a shell script, with the intent of making it readable. It is more important to me that an end-user understands what the code does, regardless of their Go expertise, than writing heavily abstracted code.

## Sequence Diagrams

The implementation of the solution is a multi container process to reduce the attack surface. I've done my best to outline the flow.

### Initialization Container

This container runs at start-up and exits when 

```mermaid
sequenceDiagram
  participant aksauditinit as aks-auditd-init Init Container
  participant workernode

  aksauditinit->>workernode: Deploy auditd, audisp-plugins
  aksauditinit->>workernode: Add new user and group to run the aks-auditd-run container as 
  aksauditinit->>workernode: Install and start aks-auditd-monitor service
```

### Run Container

This container runs on a continuous basis as the user and group created by the aks-auditd-init container.

```mermaid
sequenceDiagram
  participant aksauditdrun as aks-auditd DaemonSet
  participant workernode as Node
  participant aksauditdmonitor as aks-auditd-monitor service on node
  participant auditd as auditd service

  loop User Defined Interval
      aksauditdrun->>aksauditdrun: Check for updated rules
    opt File Changed
      aksauditdrun->>workernode: Copy updated rules
      workernode->>aksauditdmonitor: Node kernel triggers file change event
      aksauditdmonitor->>auditd: Restart Service
    end 
  end
```

Below is a flow chart for the flow of kernel audit data to a Log Analytics Workspace.

```mermaid
flowchart LR
  kernel[Linux Kernel]
  audit[auditd Service]
  syslog[Syslog]  
  ci[Container Insights POD]
  law[Log Analytics Workspace]
  kernel-->audit
  audit-->syslog
  syslog-->ci
  ci-->law
```

## FAQ

### Where do I find the audit data in the Log Analytics Workspace?

By default, the auditd data is sent to the LAW's Syslog table as part of the audisp-syslog process. The default facility is user.

The following LAW query will show this information when the data is sent to the user facility. Change the facility value if you've modified your [audisp-plugins.yaml](./kubernetes/configmap/audisp-plugins.yaml) to use a different facility.

```kusto
Syslog
| where Facility == 'user' and ProcessName == 'audisp-syslog'
```

### How do I change the syslog facility the data goes go?

Open the [audisp-plugins.yaml](./kubernetes/configmap/audisp-plugins.yaml) file and modify the config map portion that represents syslog.conf. The comments at the top of the syslog.conf section describe how to modify the target facility.

### How do I debug my deployment?

To debug your deployment, you'll want to start a debug busy box on one of your nodes to review the aks-auditd-monitor service logs, which are available in journalctl.

You should also review the logs associated with the aks-auditd-init and aks-auditd containers in their respective PODs.

### What changes occurred between the original Azure implementation and this one?

This code was originally forked from [Azure aks-auditd](https://github.com/Azure/aks-auditd), thinking that a change to the new agent would be straight-forward. However, this turned out not to be the case as modern improvements in container security and changes to the Azure Monitoring landscape dictated a larger change. 

Below are key differences between [Azure aks-auditd](https://github.com/Azure/aks-auditd) and this one.

| Category | Azure aks-auditd | This Repo | Reason |
|---|---|---|---|
| Base Image | [Alpine Linux](https://hub.docker.com/_/alpine) | [Azure Distroless Minimal](https://mcr.microsoft.com/en-us/product/azurelinux/distroless/minimal/about) | Azure distroless is a hardended, official Azure distro for containers. |
| Implementation | Shell Scripts | Go binary | Go static linking allows for a smaller attack surface. |
| Agent Reliance | Legacy OMS on VMSS | Container Insights deployed as POD | CI is the updated method to deliver log data to a Log Analytics Workspace. Agent runs as a Daemonset. Not on the VMSS. |

Another note is that this aks-auditd binary supports an init container. The init container runs as root, which is required to deploy auditd and do some initial configurations. This container exists and aks-auditd runs on a consistent basis, looking for changes to the rule files, which are defined as a ConfigMap.

In an ideal world, DaemonSets would have a one time container run that would allow software to be deployed and then just quit. However, that's not the case and this is the only way I've found to get auditd deployed and operational.
