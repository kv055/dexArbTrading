variable "hcloud_token" {
  description = "Hetzner Cloud API token (read/write)."
  type        = string
  sensitive   = true
}

variable "ssh_public_key" {
  description = "SSH public key to authorize on the server (paste the full key string, not a path)."
  type        = string
}

variable "server_name" {
  description = "Name for the server and derived resources (firewall, SSH key)."
  type        = string
  default     = "dexarbtrading"
}

variable "location" {
  description = "Hetzner datacenter location. hel1 (Helsinki) is cheapest."
  type        = string
  default     = "hel1"
}

variable "trusted_cidrs" {
  description = "CIDRs allowed to reach restricted ports (Postgres 5432, Grafana 3000, Prometheus 9090). Leave empty to keep those ports closed."
  type        = list(string)
  default     = []
}
