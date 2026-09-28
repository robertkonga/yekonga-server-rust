package yekonga

import (
	"errors"

	"github.com/robertkonga/yekonga-server-go/helper"
)

func runDefaultCloudFunctions() {
	config := Server.Config

	if config.IsAuthorizationServer {
		_protectSensitiveDataModel(DefaultAuthDatabaseStructure)
	}

	if config.HasTenantCatch {
		_protectSensitiveDataModel(DefaultTenantCatchDatabaseStructure)
	}

	if config.HasTenant {
		_protectSensitiveDataModel(DefaultTenantDatabaseStructure)

		if config.HasTenantBilling {
			_protectSensitiveDataModel(DefaultBillingDatabaseStructure)
		}
	}

	if (config.HasTenant && config.HasTenantBilling) || config.HasPaymentModule {
		_protectSensitiveDataModel(DefaultPaymentDatabaseStructure)
	}

}

func _protectSensitiveDataModel(structureType DatabaseStructure) {
	for k, _ := range structureType {
		k = helper.ToCamelCase(helper.Singularize(k))

		Server.BeforeFind(k, nil, nil, func(rc *RequestContext, qc *QueryContext) (interface{}, error) {
			return false, errors.New("Protected by default cloud function")
		})

		// Server.BeforeCreate(k, nil, nil, func(rc *RequestContext, qc *QueryContext) (interface{}, error) {
		// 	return false, errors.New("Protected by default cloud function")
		// })

		// Server.BeforeUpdate(k, nil, nil, func(rc *RequestContext, qc *QueryContext) (interface{}, error) {
		// 	return false, errors.New("Protected by default cloud function")
		// })

		// Server.BeforeDelete(k, nil, nil, func(rc *RequestContext, qc *QueryContext) (interface{}, error) {
		// 	return false, errors.New("Protected by default cloud function")
		// })
	}
}
