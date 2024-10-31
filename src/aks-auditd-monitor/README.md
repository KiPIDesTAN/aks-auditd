# AKS Audit Monitor

Code in this directory is designed to run on an AKS node as a service and monitor for changes to files in /etc/audit/rules.d and /etc/audit/plugins.d. When changes occur in this directory, the program will restart the auditd service.

