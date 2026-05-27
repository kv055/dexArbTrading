output "server_ip" {
  description = "Public IPv4 address of the provisioned server."
  value       = hcloud_server.main.ipv4_address
}

output "server_id" {
  description = "Hetzner server ID."
  value       = hcloud_server.main.id
}
