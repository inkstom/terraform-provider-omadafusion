# One laptop. Generate its key pair on the laptop and paste the public key.
resource "omadafusion_wireguard_peer" "laptop" {
  name              = "it-laptop"
  interface_id      = omadafusion_wireguard.remote.id
  public_key        = "BASE64-PUBLIC-KEY-OF-THE-LAPTOP="
  allowed_addresses = ["10.250.0.2/32"]
}
