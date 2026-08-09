resource "boomi_data_integration_logicode_file" "example" {
  filename = "transform.py"
  content  = file("${path.module}/transform.py")
}
