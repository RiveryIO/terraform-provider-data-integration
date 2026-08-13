resource "boomi_data_integration_connection" "s3" {
  name = "S3 File Zone"
  type = "aws_fz"

  parameters_json = jsonencode({
    aws_access_key    = "AKIA..."
    aws_access_secret = "..."
    region            = "us-east-1"
    bucket_name       = "my-data-bucket"
  })
}

# A source connection that stages through the S3 file zone above, linked via
# fz_connection_id.
resource "boomi_data_integration_connection" "mysql_source" {
  name             = "MySQL"
  type             = "mysql"
  fz_connection_id = boomi_data_integration_connection.s3.id

  parameters_json = jsonencode({
    host     = "db.internal"
    username = "readonly"
    password = "..."
    database = "app"
  })
}
