
# Create the resource group
resource "azurerm_resource_group" "this" {
  name     = var.resource_group_name
  location = var.location
}

# Create a Log Analytics Workspace to send the data to
resource "azurerm_log_analytics_workspace" "this" {
  name                = var.log_analytics_workspace_name
  resource_group_name = azurerm_resource_group.this.name
  location            = var.location
  sku                 = "PerGB2018"
}

# Create the Container Insights DCR that consumes stdout/stderr for containers to
# the LAW ContainerLogV2 table and AKS node syslog data to the LAW Syslog table.
resource "azurerm_monitor_data_collection_rule" "this" {
  name                  = var.data_collection_rule_name
  resource_group_name   = azurerm_resource_group.this.name
  location              = var.location

  data_sources {
    extension {
      name            = "ContainerInsightsExtension"
      streams         = [ "Microsoft-ContainerLogV2" ]
      extension_name  = "ContainerInsights"
      extension_json  = jsonencode({
        dataCollectionSettings = {
          interval                  = "1m"
          nameSpaceFilteringMode    = "Off"
          enableContainerLogV2      = "true"
        }
      })
    }

    syslog {
      facility_names    = [ "auth","authpriv","cron","daemon","mark","kern","local0","local1","local2","local3","local4","local5","local6","local7","lpr","mail","news","syslog","user","uucp" ]
      log_levels        = [ "Debug","Info","Notice","Warning","Error","Critical","Alert","Emergency" ]
      name              = "aksSyslog"
      streams           = [ "Microsoft-Syslog" ]
    }
  }

  destinations {
    log_analytics {
      workspace_resource_id = azurerm_log_analytics_workspace.this.id
      name                  = "lawWorkspace"
    }
  }

  data_flow {
      streams       = [ "Microsoft-ContainerLogV2", "Microsoft-Syslog" ]
      destinations  = [ "lawWorkspace" ]
  }
}

# Create the AKS instance
resource "azurerm_kubernetes_cluster" "this" {
  name                = var.kubernetes_cluster_name
  resource_group_name = azurerm_resource_group.this.name
  location            = var.location

  kubernetes_version                = "1.29"
  sku_tier                          = "Free"
  role_based_access_control_enabled = true
  private_cluster_enabled           = false
  dns_prefix = var.kubernetes_cluster_name
  identity {
    type = "SystemAssigned"
  }

  default_node_pool {
    name                    = "default"
    node_count              = 1
    vm_size                 = "Standard_D2_v2"
    type                    = "VirtualMachineScaleSets"
    os_sku = "Ubuntu"
  }

  oms_agent {
    msi_auth_for_monitoring_enabled  = true
    log_analytics_workspace_id       = azurerm_log_analytics_workspace.this.id
  }
}

# Associate the DCR to the AKS cluster
resource "azurerm_monitor_data_collection_rule_association" "this" {
  name                    = "${var.data_collection_rule_name}-association"
  data_collection_rule_id = azurerm_monitor_data_collection_rule.this.id
  target_resource_id      = azurerm_kubernetes_cluster.this.id
}

# Get the kubeconfig for the AKS cluster
provider "kubernetes" {
  host                   = "${azurerm_kubernetes_cluster.this.kube_config.0.host}"
  client_certificate     = "${base64decode(azurerm_kubernetes_cluster.this.kube_config.0.client_certificate)}"
  client_key             = "${base64decode(azurerm_kubernetes_cluster.this.kube_config.0.client_key)}"
  cluster_ca_certificate = "${base64decode(azurerm_kubernetes_cluster.this.kube_config.0.cluster_ca_certificate)}"
}

# Deploy the auditd-rules ConfigMap to the AKS cluster
resource "kubernetes_manifest" "auditd-rules" {
  manifest = yamldecode(file("../../kubernetes/configmap/auditd-rules.yaml"))
  depends_on = [ azurerm_kubernetes_cluster.this ]
}

# Deploy the auditd-rules ConfigMap to the AKS cluster
resource "kubernetes_manifest" "audisp-plugins" {
  manifest = yamldecode(file("../../kubernetes/configmap/audisp-plugins.yaml"))
  depends_on = [ azurerm_kubernetes_cluster.this ]
}

# Deploy the DaemonSet to the AKS cluster
resource "kubernetes_manifest" "aks-auditd-daemonset" {
  manifest = yamldecode(file("../../kubernetes/daemonset.yaml"))
  depends_on = [ azurerm_kubernetes_cluster.this ]
}

# Deploy the Container Insights ConfigMap to gather kube-system:aks-auditd logs from the AKS cluster
# These will be sent to the ContainerLogV2 table in the Log Analytics Workspace
resource "kubernetes_manifest" "containerinsights" {
  manifest = yamldecode(file("../container-azm-ms-agentconfig.yaml"))
  depends_on = [ azurerm_kubernetes_cluster.this ]
}