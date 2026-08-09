resource "boomi_data_integration_data_flow_cdc_config" "example" {
  data_flow_id = boomi_data_integration_data_flow.mysql_cdc.id

  config_json = jsonencode({
    datasource_type = "mysql"
    binlog_file     = "mysql-bin.000042"
    binlog_position = "154"
  })
}
