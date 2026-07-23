output "server_ip" {
  description = "Public IPv4 address of the provisioned server."
  value       = hcloud_server.main.ipv4_address
}

output "server_id" {
  description = "Hetzner server ID."
  value       = hcloud_server.main.id
}

output "public_hostname" {
  description = "Public hostname assigned to the VPS edge."
  value       = var.public_hostname
}
