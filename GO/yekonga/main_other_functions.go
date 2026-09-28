package yekonga

import (
	"github.com/robertkonga/yekonga-server-go/config"
	"github.com/robertkonga/yekonga-server-go/datatype"
	"github.com/robertkonga/yekonga-server-go/helper"
)

func (y *YekongaData) GetTenantConfig(req *Request) *config.TenantConfig {
	client := req.Client()
	host := client.OriginDomain()

	var tenant *datatype.DataMap
	var tenantConfig *datatype.DataMap

	if req.App.Config.HasTenant {
		tenant, tenantConfig = req.App.tenantByHost(host)
	}

	if tenant != nil {
		tenantId := helper.GetValueOf(tenant, "id")
		// console.Log("tenantConfig", tenantId, tenantConfig)

		if tenantConfig == nil {
			tenantConfig = &datatype.DataMap{
				"tenantId": tenantId,
			}
		}

		if helper.IsNotEmpty(tenantConfig) {
			port := client.Port

			if helper.IsEmpty(port) || port == "80" || port == "443" {
				port = ""
			} else {
				port = ":" + port
			}

			lightTheme := helper.GetValueOfMap(tenantConfig, "lightTheme")
			darkTheme := helper.GetValueOfMap(tenantConfig, "darkTheme")
			baseUrl := client.Proto + "://" + host + port
			logoStr := helper.GetValueOfString(lightTheme, "logo")
			logoUrl := helper.GetBaseUrl(logoStr, host)
			faviconStr := helper.GetValueOfString(lightTheme, "favicon")
			faviconUrl := helper.GetBaseUrl(faviconStr, host)
			tenantName := helper.GetValueOfString(tenant, "name")
			description := helper.GetValueOf(tenant, "description")

			if helper.IsEmpty(description) {
				description = "This is " + tenantName + " main website"
			}

			data := datatype.DataMap{
				"domain":            host,
				"tenantId":          tenantId,
				"appName":           y.Config.AppName,
				"baseUrl":           baseUrl,
				"userId":            helper.GetValueOf(tenant, "userId"),
				"tenantName":        tenantName,
				"description":       description,
				"language":          helper.GetValueOf(tenant, "language"),
				"type":              helper.GetValueOf(tenant, "type"),
				"address":           helper.GetValueOf(tenant, "address"),
				"email":             helper.GetValueOf(tenant, "email"),
				"phone":             helper.GetValueOf(tenant, "phone"),
				"logoUrl":           logoUrl,
				"faviconUrl":        faviconUrl,
				"smtp":              helper.GetValueOf(tenantConfig, "smtp"),
				"sms":               helper.GetValueOf(tenantConfig, "sms"),
				"whatsapp":          helper.GetValueOf(tenantConfig, "whatsapp"),
				"lightTheme":        lightTheme,
				"darkTheme":         darkTheme,
				"hasMembership":     helper.GetValueOf(tenantConfig, "hasMembership"),
				"publicCanRegister": helper.GetValueOf(tenantConfig, "publicCanRegister"),
			}
			// console.Log("TenantConfig.data", data)
			// console.Log("TenantConfig.tenantConfig", tenantConfig)

			config, err := helper.ConvertTo[config.TenantConfig](data)
			if err != nil {
				return nil
			}
			return &config
		}
	}

	return nil
}
