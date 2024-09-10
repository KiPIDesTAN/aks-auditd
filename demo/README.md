# End-to-end Demo

This end-to-end demo is provided with Terraform. You can run the code locally from your machine with an account that has Contributor at the subscription level.

The demo creates an AKS instance, Azure Container Registry, Log Analytics Workspace, and Data Collection Rule to send the data from AKS to the Log Analytics Workspace. 

__NOTE:__ This code spins up resources. These resources are inexpensive, but not free. Make sure to destroy them when you're done.

## Deploy the Demo

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

## Destroy the Demo

Make sure to destroy the resources when you're done.

```console
terraform destroy
```
