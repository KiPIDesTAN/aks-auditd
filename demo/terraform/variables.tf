variable "resource_group_name" {
  description = "The name of the resource group"
  type        = string
}

variable "location" {
  description = "The location of the resources"
  type        = string
}

variable "kubernetes_cluster_name" {
  description = "The name of the Kubernetes cluster"
  type        = string
}

variable "container_registry_name" {
  description = "The name of the container registry"
  type        = string
}

variable "log_analytics_workspace_name" {
  description = "The name of the Log Analytics workspace"
  type        = string
}