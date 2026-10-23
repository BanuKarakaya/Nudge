package migrations

import _ "embed"

//go:embed 006_notification_delivery.sql
var NotificationSchema string
