package yekonga

import (
	"time"

	"github.com/robertkonga/yekonga-server-go/helper"
	"github.com/robertkonga/yekonga-server-go/plugins/graphql"
	"github.com/robertkonga/yekonga-server-go/plugins/graphql/language/ast"
	"github.com/robertkonga/yekonga-server-go/plugins/mongo-driver/bson"
)

type ActionResponse struct {
	Status  bool
	Message string
	Data    interface{}
}

func (a *ActionResponse) ToMap() map[string]interface{} {
	return map[string]interface{}{
		"status":  a.Status,
		"message": a.Message,
		"data":    a.Data,
	}
}

// Define the custom Date scalar type
var ScalarDateType = graphql.NewScalar(graphql.ScalarConfig{
	Name:        "Date",
	Description: "Custom scalar type for Date",
	Serialize: func(value interface{}) interface{} {
		if t, ok := value.(time.Time); ok {
			return t.Format(time.RFC3339) // Convert time to string format
		} else if t, ok := value.(bson.DateTime); ok {
			return t.Time().Format(time.RFC3339) // Convert time to string format
		}

		return value
	},
	ParseValue: func(value interface{}) interface{} {
		result := helper.StringToDatetime(value)
		if result != nil {
			return *result
		}

		return nil
	},
	ParseLiteral: func(valueAST ast.Value) interface{} {
		result := helper.StringToDatetime(valueAST.GetValue())

		if result != nil {
			return *result
		}

		return NULLValue
	},
})

// Define the custom Date scalar type
var ScalarTimeOnlyType = graphql.NewScalar(graphql.ScalarConfig{
	Name:        "TimeOnly",
	Description: "Custom scalar type for TimeOnly",
	Serialize: func(value interface{}) interface{} {
		if t, ok := value.(time.Time); ok {
			return t.Format(time.TimeOnly) // Convert time to string format
		} else if t, ok := value.(bson.DateTime); ok {
			return t.Time().Format(time.TimeOnly) // Convert time to string format
		}

		return value
	},
	ParseValue: func(value interface{}) interface{} {
		result := helper.StringToTimeOnly(value)
		if result != nil {
			return *result
		}

		return nil
	},
	ParseLiteral: func(valueAST ast.Value) interface{} {
		result := helper.StringToTimeOnly(valueAST.GetValue())
		if result != nil {
			return *result
		}

		return NULLValue
	},
})

// Define the custom ID scalar type
var ScalarIDType = graphql.NewScalar(graphql.ScalarConfig{
	Name:        "ID",
	Description: "Custom scalar type for ID",
	Serialize: func(value interface{}) interface{} {
		if t, ok := value.(bson.ObjectID); ok {
			return t.Hex() // Convert time to string format
		}

		return value
	},
	ParseValue: func(value interface{}) interface{} {
		if str, ok := value.(string); ok {
			return helper.ObjectID(str)
		}
		return nil
	},
	ParseLiteral: func(valueAST ast.Value) interface{} {
		if strValue, ok := valueAST.GetValue().(string); ok {
			return helper.ObjectID(strValue)
		}

		return NULLValue
	},
})

// Define the custom Any scalar type
var ScalarAnyType = graphql.NewScalar(graphql.ScalarConfig{
	Name:        "Any",
	Description: "Custom scalar type for Any",
	Serialize: func(value interface{}) interface{} {
		// logger.Success("Any.Serialize", value)
		return value
	},
	ParseValue: func(value interface{}) interface{} {
		// logger.Success("Any.ParseValue", value)
		return value
	},
	ParseLiteral: func(valueAST ast.Value) interface{} {
		// logger.Success("Any.ParseLiteral", valueAST)
		return valueAST.GetValue()
	},
})

// Define the custom Array scalar type
var ScalarArrayType = graphql.NewScalar(graphql.ScalarConfig{
	Name:        "Array",
	Description: "Custom scalar type for Array",
	Serialize: func(value interface{}) interface{} {
		// logger.Success("Array.Serialize", value)

		if helper.IsArray(value) {
			return helper.ToList[interface{}](value)
		}

		return []interface{}{}
	},
	ParseValue: func(value interface{}) interface{} {
		// logger.Success("Array.ParseValue", value)

		return value
	},
	ParseLiteral: func(valueAST ast.Value) interface{} {
		// logger.Success("Array.ParseLiteral", valueAST)

		return valueAST.GetValue()
	},
})

// Define the custom Any scalar type
var ScalarStringType = graphql.NewScalar(graphql.ScalarConfig{
	Name:        "CustomString",
	Description: "Custom scalar type for String",
	Serialize: func(value interface{}) interface{} {
		return value
	},
	ParseValue: func(value interface{}) interface{} {
		return value
	},
	ParseLiteral: func(valueAST ast.Value) interface{} {
		if strValue, ok := valueAST.GetValue().(string); ok {
			return strValue
		}

		return NULLValue
	},
})

// Define Role Enum
var RoleEnum = graphql.NewEnum(graphql.EnumConfig{
	Name: "Role",
	Values: graphql.EnumValueConfigMap{
		"ADMIN": &graphql.EnumValueConfig{
			Value: "admin",
		},
		"USER": &graphql.EnumValueConfig{
			Value: "user",
		},
		"GUEST": &graphql.EnumValueConfig{
			Value: "guest",
		},
	},
})

// Define Role Enum
var GeneralOrderOptionsEnum = graphql.NewEnum(graphql.EnumConfig{
	Name: "GeneralOrderOptionsEnum",
	Values: graphql.EnumValueConfigMap{
		"ASC": &graphql.EnumValueConfig{
			Value: "ASC",
		},
		"DESC": &graphql.EnumValueConfig{
			Value: "DESC",
		},
	},
})

// Define Role Enum
var DownloadTypeOptionsEnum = graphql.NewEnum(graphql.EnumConfig{
	Name: "DownloadTypeOptionsEnum",
	Values: graphql.EnumValueConfigMap{
		"PDF": &graphql.EnumValueConfig{
			Value: "PDF",
		},
		"EXCEL": &graphql.EnumValueConfig{
			Value: "EXCEL",
		},
		"CSV": &graphql.EnumValueConfig{
			Value: "CSV",
		},
		"PRINT": &graphql.EnumValueConfig{
			Value: "PRINT",
		},
		"IMAGE": &graphql.EnumValueConfig{
			Value: "IMAGE",
		},
		"PNG": &graphql.EnumValueConfig{
			Value: "PNG",
		},
		"JPG": &graphql.EnumValueConfig{
			Value: "JPG",
		},
	},
})

// Define Role Enum
var OrientationOptionsEnum = graphql.NewEnum(graphql.EnumConfig{
	Name: "OrientationOptionsEnum",
	Values: graphql.EnumValueConfigMap{
		"PORTRAIT": &graphql.EnumValueConfig{
			Value: "PORTRAIT",
		},
		"LANDSCAPE": &graphql.EnumValueConfig{
			Value: "LANDSCAPE",
		},
	},
})

var GeneralGraphOptionsEnum = graphql.NewEnum(graphql.EnumConfig{
	Name: "GeneralGraphOptions",
	Values: graphql.EnumValueConfigMap{
		"PIE": &graphql.EnumValueConfig{
			Value: "PIE",
		},
		"DOUGHNUT": &graphql.EnumValueConfig{
			Value: "PIE",
		},
		"LINE": &graphql.EnumValueConfig{
			Value: "LINE",
		},
		"BAR": &graphql.EnumValueConfig{
			Value: "BAR",
		},
	},
})

var GeneralDownloadOutput = graphql.ObjectConfig{
	Name: "GeneralDownloadOutput",
	Fields: graphql.Fields{
		"filename": &graphql.Field{
			Type: graphql.String,
		},
		"url": &graphql.Field{
			Type: graphql.String,
		},
		"type": &graphql.Field{
			Type: graphql.String,
		},
		"size": &graphql.Field{
			Type: graphql.Float,
		},
	},
}

var GeneralDownloadTypeInput = graphql.NewInputObject(graphql.InputObjectConfig{
	Name: "GeneralDownloadTypeInput",
	Fields: graphql.InputObjectConfigFieldMap{
		"operationName": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
		"variables": &graphql.InputObjectFieldConfig{
			Type: GeneralDownloadVariablesTypeInput,
		},
		"query": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
	},
})

var GeneralDownloadVariablesTypeInput = graphql.NewInputObject(graphql.InputObjectConfig{
	Name: "GeneralDownloadVariablesTypeInput",
	Fields: graphql.InputObjectConfigFieldMap{
		"input": &graphql.InputObjectFieldConfig{
			Type: ScalarAnyType,
		},
	},
})

var UsernameIdentifierEnum = graphql.NewEnum(graphql.EnumConfig{
	Name: "UsernameIdentifier",
	Values: graphql.EnumValueConfigMap{
		"phone": &graphql.EnumValueConfig{
			Value: "phone",
		},
		"email": &graphql.EnumValueConfig{
			Value: "email",
		},
		"whatsapp": &graphql.EnumValueConfig{
			Value: "whatsapp",
		},
	},
})

var GeneralTotalOptionsEnum = graphql.NewEnum(graphql.EnumConfig{
	Name: "GeneralTotalOptions",
	Values: graphql.EnumValueConfigMap{
		"COUNT": &graphql.EnumValueConfig{
			Value: "COUNT",
		},
		"MIN": &graphql.EnumValueConfig{
			Value: "MIN",
		},
		"MAX": &graphql.EnumValueConfig{
			Value: "MAX",
		},
		"SUM": &graphql.EnumValueConfig{
			Value: "SUM",
		},
		"AVG": &graphql.EnumValueConfig{
			Value: "AVG",
		},
		"AVERAGE": &graphql.EnumValueConfig{
			Value: "AVERAGE",
		},
	},
})

var GeneralPeriodicityEnum = graphql.NewEnum(graphql.EnumConfig{
	Name: "GeneralPeriodicity",
	Values: graphql.EnumValueConfigMap{
		"HOURLY": &graphql.EnumValueConfig{
			Value: "HOURLY",
		},
		"DAILY": &graphql.EnumValueConfig{
			Value: "DAILY",
		},
		"DAY_HOURS": &graphql.EnumValueConfig{
			Value: "DAY_HOURS",
		},
		"WEEKLY": &graphql.EnumValueConfig{
			Value: "WEEKLY",
		},
		"WEEK_DAYS": &graphql.EnumValueConfig{
			Value: "WEEK_DAYS",
		},
		"MONTHLY": &graphql.EnumValueConfig{
			Value: "MONTHLY",
		},
		"MONTH_DAYS": &graphql.EnumValueConfig{
			Value: "MONTH_DAYS",
		},
		"QUARTERLY": &graphql.EnumValueConfig{
			Value: "QUARTERLY",
		},
		"SEMIANNUALLY": &graphql.EnumValueConfig{
			Value: "SEMIANNUALLY",
		},
		"YEARLY": &graphql.EnumValueConfig{
			Value: "YEARLY",
		},
		"YEAR_MONTHS": &graphql.EnumValueConfig{
			Value: "YEAR_MONTHS",
		},
		"NONE": &graphql.EnumValueConfig{
			Value: "NONE",
		},
	},
})

var GraphDataType = graphql.NewObject(graphql.ObjectConfig{
	Name: "GraphDataType",
	Fields: graphql.Fields{
		"labels": &graphql.Field{
			Type: graphql.NewList(graphql.String),
		},
		"datasets": &graphql.Field{
			Type: graphql.NewList(GraphDataset),
		},
	},
})

var GraphDataset = graphql.NewObject(graphql.ObjectConfig{
	Name: "GraphDataset",
	Fields: graphql.Fields{
		"label": &graphql.Field{
			Type: graphql.String,
		},
		"color": &graphql.Field{
			Type: graphql.NewList(graphql.String),
		},
		"backgroundColor": &graphql.Field{
			Type: graphql.NewList(graphql.String),
		},
		"data": &graphql.Field{
			Type: graphql.NewList(graphql.Int),
		},
	},
})

var ActionResponseType = graphql.NewObject(graphql.ObjectConfig{
	Name: "ActionResponse",
	Fields: graphql.Fields{
		"status": &graphql.Field{
			Type: graphql.Boolean,
		},
		"message": &graphql.Field{
			Type: graphql.String,
		},
		"data": &graphql.Field{
			Type: ScalarAnyType,
		},
	},
})

var ConfirmTokenResponseType = graphql.NewObject(graphql.ObjectConfig{
	Name: "ConfirmTokenResponse",
	Fields: graphql.Fields{
		"status": &graphql.Field{
			Type: graphql.Boolean,
		},
		"email": &graphql.Field{
			Type: graphql.String,
		},
		"token": &graphql.Field{
			Type: graphql.String,
		},
	},
})

var UserProfileType = graphql.NewObject(graphql.ObjectConfig{
	Name: "Profile",
	Fields: graphql.Fields{
		"id": &graphql.Field{
			Type: ScalarIDType,
		},
		"userId": &graphql.Field{
			Type: ScalarIDType,
		},
		"username": &graphql.Field{
			Type: graphql.String,
		},
		"usernameType": &graphql.Field{
			Type: graphql.String,
		},
		"phone": &graphql.Field{
			Type: graphql.String,
		},
		"phoneVerifiedAt": &graphql.Field{
			Type: ScalarDateType,
		},
		"phoneVerifyCode": &graphql.Field{
			Type: graphql.String,
		},
		"email": &graphql.Field{
			Type: graphql.String,
		},
		"emailVerifiedAt": &graphql.Field{
			Type: ScalarDateType,
		},
		"emailVerifyCode": &graphql.Field{
			Type: graphql.Boolean,
		},
		"whatsapp": &graphql.Field{
			Type: graphql.String,
		},
		"whatsappVerifiedAt": &graphql.Field{
			Type: ScalarDateType,
		},
		"whatsappVerifyCode": &graphql.Field{
			Type: graphql.String,
		},
		"profileUrl": &graphql.Field{
			Type: graphql.String,
		},
		"firstName": &graphql.Field{
			Type: graphql.String,
		},
		"secondName": &graphql.Field{
			Type: graphql.String,
		},
		"lastName": &graphql.Field{
			Type: graphql.String,
		},
		"userType": &graphql.Field{
			Type: graphql.String,
		},
		"dateOfBirth": &graphql.Field{
			Type: ScalarDateType,
		},
		"deviceToken": &graphql.Field{
			Type: graphql.String,
		},
		"gender": &graphql.Field{
			Type: graphql.String,
		},
		"googleToken": &graphql.Field{
			Type: graphql.String,
		},
		"isEmailVerified": &graphql.Field{
			Type: graphql.Boolean,
		},
		"isPhoneVerified": &graphql.Field{
			Type: graphql.Boolean,
		},
		"isWhatsappVerified": &graphql.Field{
			Type: graphql.Boolean,
		},
		"lastActive": &graphql.Field{
			Type: ScalarDateType,
		},
		"role": &graphql.Field{
			Type: graphql.String,
		},
		"status": &graphql.Field{
			Type: graphql.String,
		},
		"token": &graphql.Field{
			Type: graphql.String,
		},

		"countryId": &graphql.Field{
			Type: graphql.String,
		},
		"regionId": &graphql.Field{
			Type: graphql.String,
		},
		"districtId": &graphql.Field{
			Type: graphql.String,
		},
		"wardId": &graphql.Field{
			Type: graphql.String,
		},
		"isActive": &graphql.Field{
			Type: graphql.Boolean,
		},
		"isBanned": &graphql.Field{
			Type: graphql.Boolean,
		},
		"isAdmin": &graphql.Field{
			Type: graphql.Boolean,
		},
		"isManager": &graphql.Field{
			Type: graphql.Boolean,
		},
		"owner": &graphql.Field{
			Type: graphql.Boolean,
		},
		"profileId": &graphql.Field{
			Type: graphql.String,
		},
		"profileRole": &graphql.Field{
			Type: graphql.String,
		},
		"profileName": &graphql.Field{
			Type: graphql.String,
		},
		"updatedAt": &graphql.Field{
			Type: ScalarDateType,
		},
		"createdAt": &graphql.Field{
			Type: ScalarDateType,
		},
		"deletedAt": &graphql.Field{
			Type: ScalarDateType,
		},
	},
})

var CredentialTokenType = graphql.NewObject(graphql.ObjectConfig{
	Name: "CredentialToken",
	Fields: graphql.Fields{
		"accessToken": &graphql.Field{
			Type: ScalarIDType,
		},
		"refreshToken": &graphql.Field{
			Type: graphql.String,
		},
	},
})

// =========================================================================== //

var UserProfileInput = graphql.NewInputObject(graphql.InputObjectConfig{
	Name: "ProfileInput",
	Fields: graphql.InputObjectConfigFieldMap{
		"id": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
		"username": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
		"usernameType": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
		"phone": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
		"email": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
	},
})

var OtpInput = graphql.NewInputObject(graphql.InputObjectConfig{
	Name: "OtpInput",
	Fields: graphql.InputObjectConfigFieldMap{
		"usernameType": &graphql.InputObjectFieldConfig{
			Type: UsernameIdentifierEnum,
		},
		"username": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
	},
})

var LoginTypeEnum = graphql.NewEnum(graphql.EnumConfig{
	Name: "LoginTypeEnum",
	Values: graphql.EnumValueConfigMap{
		"PASSWORD": &graphql.EnumValueConfig{
			Value: "password",
		},
		"OTP": &graphql.EnumValueConfig{
			Value: "otp",
		},
		"REGISTRATION": &graphql.EnumValueConfig{
			Value: "registration",
		},
		"NORMAL": &graphql.EnumValueConfig{
			Value: "normal",
		},
	},
})

// "NGO", "COMPANY", "PRIVATE"
var OrganizationTypeEnum = graphql.NewEnum(graphql.EnumConfig{
	Name: "OrganizationTypeEnum",
	Values: graphql.EnumValueConfigMap{
		"NGO": &graphql.EnumValueConfig{
			Value: "ngo",
		},
		"COMPANY": &graphql.EnumValueConfig{
			Value: "company",
		},
		"PRIVATE": &graphql.EnumValueConfig{
			Value: "private",
		},
	},
})

var LoginInput = graphql.NewInputObject(graphql.InputObjectConfig{
	Name: "LoginInput",
	Fields: graphql.InputObjectConfigFieldMap{
		"usernameType": &graphql.InputObjectFieldConfig{
			Type: UsernameIdentifierEnum,
		},
		"username": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
		"password": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
		"type": &graphql.InputObjectFieldConfig{
			Type: LoginTypeEnum,
		},
		"moduleName": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
		"rememberMe": &graphql.InputObjectFieldConfig{
			Type: graphql.Boolean,
		},
	},
})

var SocialLoginInput = graphql.NewInputObject(graphql.InputObjectConfig{
	Name: "SocialLoginInput",
	Fields: graphql.InputObjectConfigFieldMap{
		"authUser": &graphql.InputObjectFieldConfig{
			Type: ScalarAnyType,
		},
		"expiresIn": &graphql.InputObjectFieldConfig{
			Type: graphql.Int,
		},
		"prompt": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
		"scope": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
		"accessToken": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
		"tokenType": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
	},
})

var ConcatOtpInput = graphql.NewInputObject(graphql.InputObjectConfig{
	Name: "ConcatOtpInput",
	Fields: graphql.InputObjectConfigFieldMap{
		"userId": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
		"phone": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
		"email": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
		"whatsapp": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
	},
})

var ContactVerifyInput = graphql.NewInputObject(graphql.InputObjectConfig{
	Name: "ContactVerifyInput",
	Fields: graphql.InputObjectConfigFieldMap{
		"userId": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
		"phone": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
		"email": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
		"whatsapp": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
		"password": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
		"type": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
	},
})

var ResetPasswordInput = graphql.NewInputObject(graphql.InputObjectConfig{
	Name: "ResetPasswordInput",
	Fields: graphql.InputObjectConfigFieldMap{
		"username": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
	},
})

var ConfirmTokenInput = graphql.NewInputObject(graphql.InputObjectConfig{
	Name: "ConfirmTokenInput",
	Fields: graphql.InputObjectConfigFieldMap{
		"token": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
	},
})

var ChangePasswordInput = graphql.NewInputObject(graphql.InputObjectConfig{
	Name: "ChangePasswordInput",
	Fields: graphql.InputObjectConfigFieldMap{
		"token": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
		"password": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
		"passwordConfirmation": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
	},
})

var RegistrationInput = graphql.NewInputObject(graphql.InputObjectConfig{
	Name: "RegistrationInput",
	Fields: graphql.InputObjectConfigFieldMap{
		"userId": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
		"firstName": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
		"lastName": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
		"organization": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
		"description": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
		"logoUrl": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
		"type": &graphql.InputObjectFieldConfig{
			Type: OrganizationTypeEnum,
		},
		"address": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
		"email": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
		"phone": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
		"website": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
		"domain": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
		"subdomain": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
		"language": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
		"isApproved": &graphql.InputObjectFieldConfig{
			Type: graphql.Boolean,
		},
		"status": &graphql.InputObjectFieldConfig{
			Type: graphql.String,
		},
	},
})
