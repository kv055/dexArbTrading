terraform {
  required_providers {
    hcloud = {
      source  = "hetznercloud/hcloud"
      version = "~> 1.49"
    }
  }
  required_version = ">= 1.5"
}

provider "hcloud" {
  token = var.hcloud_token
}

resource "hcloud_ssh_key" "deploy" {
  name       = "${var.server_name}-deploy"
  public_key = var.ssh_public_key
}

resource "hcloud_firewall" "main" {
  name = "${var.server_name}-fw"

  rule {
    direction  = "in"
    protocol   = "tcp"
    port       = "22"
    source_ips = ["0.0.0.0/0", "::/0"]
  }

  rule {
    direction  = "in"
    protocol   = "tcp"
    port       = "5000"
    source_ips = ["0.0.0.0/0", "::/0"]
  }

  rule {
    direction  = "in"
    protocol   = "tcp"
    port       = "5010"
    source_ips = ["0.0.0.0/0", "::/0"]
  }

  # Postgres — only open if trusted_cidrs are provided
  dynamic "rule" {
    for_each = length(var.trusted_cidrs) > 0 ? [1] : []
    content {
      direction  = "in"
      protocol   = "tcp"
      port       = "5432"
      source_ips = var.trusted_cidrs
    }
  }

  # Grafana — only open if trusted_cidrs are provided
  dynamic "rule" {
    for_each = length(var.trusted_cidrs) > 0 ? [1] : []
    content {
      direction  = "in"
      protocol   = "tcp"
      port       = "3000"
      source_ips = var.trusted_cidrs
    }
  }

  # Prometheus — only open if trusted_cidrs are provided
  dynamic "rule" {
    for_each = length(var.trusted_cidrs) > 0 ? [1] : []
    content {
      direction  = "in"
      protocol   = "tcp"
      port       = "9090"
      source_ips = var.trusted_cidrs
    }
  }
}

resource "hcloud_server" "main" {
  name        = var.server_name
  server_type = "cx33"
  image       = "debian-12"
  location    = var.location
  ssh_keys    = [hcloud_ssh_key.deploy.id]

  firewall_ids = [hcloud_firewall.main.id]

  # Create the deploy user and authorize the same key as root.
  # Docker install and further config is handled by Ansible.
  user_data = <<-EOF
    #cloud-config
    hostname: ${var.server_name}
    users:
      - name: deploy
        groups: sudo
        shell: /bin/bash
        sudo: ALL=(ALL) NOPASSWD:ALL
        ssh_authorized_keys:
          - ${var.ssh_public_key}
    package_update: false
  EOF

  labels = {
    project = "dexarbtrading"
    managed = "terraform"
  }
}
