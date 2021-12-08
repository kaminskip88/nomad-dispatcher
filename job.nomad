job "test" {
  datacenters = ["dc1"]
  type        = "batch"

  parameterized {
    payload = "required"
    meta_required = [
        "META_DATA_1",
        "META_DATA_2"
    ]
  }

  group "test" {
    count = 1
    restart {
      attempts = 0
    }
    reschedule {
      attempts = 0
    }
    ephemeral_disk {
      size = 200
    }
    task "alpine" {
      kill_timeout = "10s"
      driver       = "docker"
      config {
        image = "alpine"
        command = "sh"
        args = [
          "/local/test.sh"
        ]
      }
      template {
        destination = "local/test.sh"
        data        = <<-EOT
        echo "$${NOMAD_META_META_DATA_1}"
        echo "=========================="
        echo "$${NOMAD_META_META_DATA_2}"
        echo "=========================="
        head -1 /local/payload.txt
        EOT
      }
      dispatch_payload {
        file = "payload.txt"
      }
      resources {
        cpu    = 200
        memory = 256
      }

      logs {
        max_files     = 2
        max_file_size = 50
      }
    }
  }
}