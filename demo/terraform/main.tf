
# Create the resource group
resource "azurerm_resource_group" "this" {
  name     = var.resource_group_name
  location = var.location
}

# Create the Azure Container Registry
resource "azurerm_container_registry" "this" {
  location            = var.location
  name                = var.container_registry_name
  resource_group_name = azurerm_resource_group.this.name
  sku                 = "Standard"
}

# Provide AKS access to pull from the registry
resource "azurerm_role_assignment" "acr" {
  principal_id                     = azurerm_kubernetes_cluster.this.kubelet_identity[0].object_id
  scope                            = azurerm_container_registry.this.id
  role_definition_name             = "AcrPull"
  skip_service_principal_aad_check = true
}

# Crete a Log Analytics Workspace to send the data to
resource "azurerm_log_analytics_workspace" "this" {
  name                = var.log_analytics_workspace_name
  resource_group_name = azurerm_resource_group.this.name
  location            = var.location
  sku                 = "PerGB2018"
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
    node_count              = 2
    vm_size                 = "Standard_D2_v2"
    enable_auto_scaling     = false
    os_disk_size_gb         = 128
    type                    = "VirtualMachineScaleSets"
    os_sku = "Ubuntu"
  }

  oms_agent {
    msi_auth_for_monitoring_enabled  = true
    log_analytics_workspace_id          = azurerm_log_analytics_workspace.this.id
  }
}