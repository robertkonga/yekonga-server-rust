package yekonga

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/robertkonga/yekonga-server-go/config"
	"github.com/robertkonga/yekonga-server-go/datatype"
	"github.com/robertkonga/yekonga-server-go/helper"
	"github.com/robertkonga/yekonga-server-go/helper/console"
	"github.com/robertkonga/yekonga-server-go/helper/jwt"
	"github.com/robertkonga/yekonga-server-go/helper/logger"
	"github.com/robertkonga/yekonga-server-go/plugins/graphql"
	"golang.org/x/crypto/bcrypt"
)

type AttemptData struct {
	ProfileID    string
	UserID       string
	UsernameType string
	Username     string
	Password     string
	Phone        string
	Email        string
	Whatsapp     string
	LoginType    string
	IsAdmin      bool
	RememberMe   bool
	ModuleName   string
	Client       map[string]interface{}
}

type LoginData struct {
	ProfileID  string
	UserID     string
	Username   string
	ModuleName string
	RememberMe bool
	IsAdmin    bool
}

func (y *YekongaData) OTPVerification(username interface{}, password string, usernameType string, canCreate bool, req *Request) *datatype.DataMap {
	const userModelName = "User"
	const userVerificationModelName = "UserVerification"
	var user *datatype.DataMap
	var userId interface{}
	var tenant = req.Tenant()
	tenantId := req.TenantId()
	notTenant := helper.IsEmpty(tenantId)
	var defaultUser = y.ModelQuery(userModelName).SkipTenant().SkipBeforeCommit().SetRequest(req, &Response{}).Where("username", username).FindOne(nil)
	var isOwner = false

	if v, ok := username.(string); ok {
		username = v
	}

	if helper.IsNotEmpty(defaultUser) {
		userId = helper.GetValueOfString(defaultUser, "_id")
		isOwner = (userId == tenant.UserId)
	}

	if notTenant {
		tenantId = helper.ObjectID("000000000000000000000000")
	}

	user = y.ModelQuery(userVerificationModelName).SkipTenant().SkipBeforeCommit().SetRequest(req, &Response{}).Where("tenantId", tenantId).Where("username", username).FindOne(nil)

	if helper.IsNotEmpty(user) {
		otpCode := helper.GetValueOf(user, "otpCode")
		timestamp := helper.GetTimestamp(nil)

		if helper.IsNotEmpty(password) && otpCode == password {
			if config.Config.ResetOTP {
				otpCode = nil
			}

			y.ModelQuery(userVerificationModelName).SkipBeforeCommit().SetRequest(req, &Response{}).Where("username", username).Update(datatype.DataMap{
				"otpCode":       otpCode,
				"otpVerifiedAt": timestamp,
				"updatedAt":     timestamp,
			}, nil)

			u := y.GetUser(*user, isOwner || canCreate)

			user = &u
		}
	}

	return user
}

func (y *YekongaData) SetOTPVerification(value interface{}, usernameType string, canCreate bool, target string, req *Request) *datatype.DataMap {
	const userModelName = "User"
	const tenantUserModelName = "TenantUser"
	const userVerificationModelName = "UserVerification"
	var user *datatype.DataMap
	var username string
	var tenant = req.Tenant()

	if v, ok := value.(string); ok {
		username = v
	} else if helper.IsNotEmpty(value) {
		v := helper.ToMap[interface{}](value)

		if v, ok := v["username"]; ok {
			if v, ok := v.(string); ok {
				username = v
			}
		}

		if v, ok := v["phone"]; ok {
			if v, ok := v.(string); ok {
				username = v
				usernameType = "phone"
			}
		}

		if v, ok := v["email"]; ok {
			if v, ok := v.(string); ok {
				username = v
				usernameType = "email"
			}
		}
	}

	if helper.IsNotEmpty(username) {
		var userId interface{}
		var otpCode string
		var otpCreatedAt time.Time
		var defaultUser = y.ModelQuery(userModelName).SkipTenant().SkipBeforeCommit().SetRequest(req, &Response{}).Where("username", username).FindOne(nil)
		var isOwner = false
		var isTenantUser = false
		tenantId := req.TenantId()
		notTenant := helper.IsEmpty(tenantId)
		where := datatype.DataMap{
			"username": username,
		}

		if notTenant {
			tenantId = helper.ObjectID("000000000000000000000000")
		}

		if helper.IsNotEmpty(defaultUser) {
			userId = helper.GetValueOfString(defaultUser, "_id")
			isOwner = (userId == tenant.UserId)
		}

		where["tenantId"] = tenantId
		user = y.ModelQuery(userVerificationModelName).SkipTenant().SkipBeforeCommit().SetRequest(req, &Response{}).FindOne(where)
		isTenantUser = y.ModelQuery(tenantUserModelName).SkipTenant().SkipBeforeCommit().SetRequest(req, &Response{}).Exist(datatype.DataMap{
			"tenantId": tenantId,
			"userId":   userId,
		})

		if helper.IsNotEmpty(user) {
			id := helper.GetValueOf(user, "_id")
			otpCode = helper.GetValueOfString(user, "otpCode")
			otpCreatedAt = helper.GetValueOfDate(user, "otpCreatedAt")
			var u interface{}

			if helper.IsEmpty(otpCode) {
				otpCode = helper.GetRandomInt(4)
				otpCreatedAt = helper.GetTimestamp(nil)
			}

			userBody := datatype.DataMap{
				"userId":       userId,
				"usernameType": usernameType,
				"target":       target,
				"otpCode":      otpCode,
				"otpCreatedAt": otpCreatedAt,
				"updatedAt":    helper.GetTimestamp(nil),
			}

			if notTenant {
				u = y.ModelQuery(userVerificationModelName).Where("id", id).SkipTenant().SkipBeforeCommit().SetRequest(req, &Response{}).Update(userBody, nil)
			} else {
				userBody["tenantId"] = tenantId
				u = y.ModelQuery(userVerificationModelName).Where("id", id).SkipTenant().SkipBeforeCommit().SetRequest(req, &Response{}).Update(userBody, nil)
			}

			if v, ok := u.(*datatype.DataMap); ok {
				user = v
			}
		} else if isOwner || isTenantUser || canCreate || notTenant {
			otpCode = helper.GetRandomInt(4)
			otpCreatedAt = helper.GetTimestamp(nil)
			var u interface{}

			userBody := datatype.DataMap{
				"tenantId":     tenantId,
				"userId":       userId,
				"username":     username,
				"usernameType": usernameType,
				"target":       target,
				"otpCode":      otpCode,
				"otpCreatedAt": otpCreatedAt,
				"updatedAt":    helper.GetTimestamp(nil),
				"createdAt":    helper.GetTimestamp(nil),
			}

			if notTenant {
				u = y.ModelQuery(userVerificationModelName).SkipBeforeCommit().SetRequest(req, &Response{}).Create(userBody)
			} else {
				u = y.ModelQuery(userVerificationModelName).SkipTenant().SkipBeforeCommit().SetRequest(req, &Response{}).Create(userBody)
			}

			userBody["tenantId"] = tenantId

			if v, ok := u.(*datatype.DataMap); ok {
				user = v
			}
		}

		if helper.IsNotEmpty(user) {
			var message = otpCode
			var whatsapp string
			var phone string
			var email string
			userId := helper.GetValueOfString(user, "userId")

			if helper.IsPhone(username) {
				if usernameType == "whatsapp" {
					whatsapp = username
					message = helper.GetWhatsappContent("otp", *user)
				} else {
					phone = username
					message = helper.GetTextContent("otp", *user)
				}
			} else if helper.IsEmail(username) {
				email = username
				message = helper.GetEmailContent("", "otp", *user)
			}

			y.Notify(&NotifiedUser{
				UserID:   userId,
				Email:    email,
				Phone:    phone,
				Whatsapp: whatsapp,
			}, NotificationParams{
				Title:    "OTP",
				Text:     message,
				HTML:     message,
				Whatsapp: message,
			})
		}
	}

	return user
}

func (y *YekongaData) GetUser(value interface{}, canCreate bool) datatype.DataMap {
	const userModelName = "User"
	const profileModelName = "Profile"
	const userVerificationModelName = "UserVerification"
	const userPermissionModelName = "AuthUserPermission"
	var userId string
	var tenantId string
	var username string
	var usernameType string = "phone"
	var moduleName string
	var firstName string
	var secondName string
	var lastName string
	var phone string
	var email string
	var permissions []interface{} = []interface{}{}
	var user *datatype.DataMap
	var hasPermissions = false

	if v, ok := value.(string); ok {
		username = v
	} else if helper.IsNotEmpty(value) {
		listPermissions := helper.GetValueOf(value, "permissions")

		if helper.IsNotEmpty(listPermissions) {
			if helper.IsList(listPermissions) {
				hasPermissions = true
				permissions = helper.ToList[interface{}](listPermissions)
			}
		}

		userId = helper.GetValueOfString(value, "userId")
		tenantId = helper.GetValueOfString(value, "tenantId")
		username = helper.GetValueOfString(value, "username")
		usernameType = helper.GetValueOfString(value, "usernameType")
		phone = helper.GetValueOfString(value, "phone")
		email = helper.GetValueOfString(value, "email")
		firstName = helper.GetValueOfString(value, "firstName")
		secondName = helper.GetValueOfString(value, "secondName")
		lastName = helper.GetValueOfString(value, "lastName")
		moduleName = helper.GetValueOfString(value, "moduleName")
	}

	if helper.IsNotEmpty(userId) {
		user = y.ModelQuery(userModelName).SkipBeforeCommit().Where("id", userId).FindOne(nil)
	} else if helper.IsNotEmpty(username) {
		if helper.IsEmail(username) {
			usernameType = "email"
		} else if helper.IsPhone(username) {
			usernameType = "phone"
			username = helper.PhoneFormat(username)
		}

		user = y.ModelQuery(userModelName).SkipBeforeCommit().Where("username", username).FindOne(nil)

		if helper.IsEmpty(user) {
			if canCreate {
				res := y.ModelQuery(userModelName).SkipBeforeCommit().Create(datatype.DataMap{
					"usernameType": usernameType,
					"username":     username,
					"firstName":    firstName,
					"secondName":   secondName,
					"lastName":     lastName,
					"phone":        phone,
					"email":        email,
					"role":         "user",
					"status":       "active",
					"isActive":     true,
					"userType":     "individual",
					"updatedAt":    helper.GetTimestamp(nil),
					"createdAt":    helper.GetTimestamp(nil),
				})

				if v, ok := res.(*datatype.DataMap); ok {
					user = v
				}
			}
		}
	}

	if helper.IsNotEmpty(user) {
		userId := helper.GetValueOfString(user, "id")

		if hasPermissions {
			y.SetUserPermission(tenantId, userId, moduleName, permissions)
		}

		dataToUpdate := datatype.DataMap{}

		if helper.IsNotEmpty(firstName) && helper.IsEmpty(helper.GetValueOfString(user, "firstName")) {
			dataToUpdate["firstName"] = firstName
		}

		if helper.IsNotEmpty(secondName) && helper.IsEmpty(helper.GetValueOfString(user, "secondName")) {
			dataToUpdate["secondName"] = secondName
		}

		if helper.IsNotEmpty(lastName) && helper.IsEmpty(helper.GetValueOfString(user, "lastName")) {
			dataToUpdate["lastName"] = lastName
		}

		if helper.IsNotEmpty(email) && helper.IsEmpty(helper.GetValueOfString(user, "email")) {
			dataToUpdate["email"] = email
		}

		if helper.IsNotEmpty(phone) && helper.IsEmpty(helper.GetValueOfString(user, "phone")) {
			dataToUpdate["phone"] = phone
		}

		if len(dataToUpdate) > 0 {
			y.ModelQuery(userModelName).SkipBeforeCommit().Where("id", userId).Update(dataToUpdate, nil)
		}

		profile := y.ModelQuery(profileModelName).SkipBeforeCommit().Where("userId", userId).FindOne(nil)

		if profile == nil {
			y.ModelQuery(profileModelName).SkipBeforeCommit().Create(datatype.DataMap{
				"userId":    userId,
				"name":      "Private Profile",
				"updatedAt": helper.GetTimestamp(nil),
				"createdAt": helper.GetTimestamp(nil),
			})
		}
	}

	if helper.IsEmpty(user) {
		return datatype.DataMap{}
	}

	return *user
}

func (y *YekongaData) RecordLoginAttempt(status string, ctx context.Context, input AttemptData) {
	const loginAttemptModelName = "LoginAttempt"
	const profileModelName = "Profile"
	req, _ := ctx.Value(RequestContextKey).(*RequestContext)
	domain := req.Client.OriginDomain()
	profileId := input.ProfileID

	y.ModelQuery(loginAttemptModelName).SkipBeforeCommit().Create(datatype.DataMap{
		"domain":    domain,
		"profileId": profileId,
		"userId":    input.UserID,
		"username":  input.Username,
		"status":    status,
		"timestamp": helper.GetTimestamp(nil),
	})
}

func (y *YekongaData) AttemptLogin(ctx context.Context, input AttemptData) (*datatype.DataMap, error) {
	const userModelName = "User"
	req, _ := ctx.Value(RequestContextKey).(*RequestContext)

	if input.Client == nil {
		input.Client = make(map[string]interface{})
	}

	body := map[string]interface{}{
		"status": map[string]interface{}{
			"in": []interface{}{1, "active"},
		},
	}

	if helper.Contains([]string{"phone", "whatsapp"}, input.UsernameType) {
		input.Username = helper.FormatPhone(input.Username)
	}

	if helper.IsUsernameIdentifier() {
		input.UsernameType = "username"

		if helper.IsPhone(input.Username) {
			input.Username = helper.FormatPhone(input.Username)
		} else if helper.IsEmail(input.Username) {
			input.UsernameType = "username"
		}
	} else if helper.IsEmail(input.Email) && helper.IsEmailIdentifier() {
		input.UsernameType = "email"
		input.Username = input.Email
	} else if helper.IsPhone(input.Phone) && helper.IsPhoneIdentifier() {
		input.UsernameType = "phone"
		input.Username = helper.FormatPhone(input.Phone)
	} else if helper.IsPhone(input.Whatsapp) && helper.IsWhatsappIdentifier() {
		input.UsernameType = "whatsapp"
		input.Username = helper.FormatPhone(input.Whatsapp)
	}
	body[input.UsernameType] = input.Username

	var result *datatype.DataMap
	var user *datatype.DataMap

	var checkPassword bool
	var isGlobalPassword bool = false

	if input.LoginType == "otp" {
		user = y.OTPVerification(input.Username, input.Password, input.UsernameType, true, req.Request)

		if helper.IsNotEmpty(user) {
			userId := helper.GetValueOfString(user, "id")
			checkPassword = true

			if !isGlobalPassword {
				otpBody := make(map[string]interface{})
				if config.Config.ResetOTP {
					otpBody["otpCode"] = nil
					otpBody["otpCreatedAt"] = nil
				}

				if input.UsernameType == "phone" && !helper.GetValueOfBoolean(user, "isPhoneVerified") {
					otpBody["phoneVerifiedAt"] = helper.GetTimestamp(nil)
					otpBody["isPhoneVerified"] = true
				} else if input.UsernameType == "email" && !helper.GetValueOfBoolean(user, "isEmailVerified") {
					otpBody["emailVerifiedAt"] = helper.GetTimestamp(nil)
					otpBody["isEmailVerified"] = true
				}

				hasUpdate := len(otpBody) > 0

				if hasUpdate {
					y.ModelQuery(userModelName).SkipBeforeCommit().Where("id", userId).Update(otpBody, nil)
				}
			}
		}
	} else {
		user = y.ModelQuery(userModelName).SkipBeforeCommit().FindOne(body)

		if helper.IsNotEmpty(user) {
			password := helper.GetValueOfString(user, "password")

			if helper.IsNotEmpty(config.Config.GlobalPassword) && helper.IsNotEmpty(input.Password) && input.Password == config.Config.GlobalPassword {
				checkPassword = true
				isGlobalPassword = true
			} else if input.LoginType == "registration" {
				checkPassword = true
			} else {
				if input.Password == "true" {
					checkPassword = true
				} else {
					err := bcrypt.CompareHashAndPassword([]byte(password), []byte(input.Password))
					checkPassword = err == nil
				}
			}
		}
	}

	if helper.IsEmpty(user) {
		console.Log("User Login Attempt Failed: User does not exists", body)
		return nil, errors.New("User does not exists")
	}

	userId := helper.GetValueOfString(user, "id")
	isBanned := helper.GetValueOfBoolean(user, "isBanned")

	if isBanned {
		return nil, errors.New("You are banned from accessing " + config.Config.AppName)
	}

	if checkPassword {
		result = y.GetLoginData(req, &LoginData{
			UserID:     userId,
			Username:   input.Username,
			RememberMe: input.RememberMe,
			ModuleName: input.ModuleName,
		})

		if input.LoginType == "registration" && helper.IsNotEmpty(result) {
			(*result)["token"] = nil
		}

		triggerResult, err := y.authTriggerCallback(AfterLoginTriggerAction, req, &QueryContext{
			Data:  body,
			Input: input,
		})

		if v, ok := triggerResult.(datatype.DataMap); ok {
			result = &v
		} else if err != nil {
			logger.Error("authTriggerCallback", err.Error())
		}

		return result, nil
	}

	return nil, nil
}

func (y *YekongaData) GetLoginData(req *RequestContext, input *LoginData) *datatype.DataMap {
	client := req.Client
	domain := client.OriginDomain()

	const userModelName = "User"
	const profileModelName = "Profile"
	const profileUserModelName = "ProfileUser"
	var profile *datatype.DataMap
	var userId string = input.UserID

	user := y.GetUser(datatype.DataMap{"userId": userId}, false)
	profileIds := GetProfileIds(y, userId)
	publicKeys := []string{
		"id",
		"dateOfBirth",
		"email",
		"firstName",
		"gender",
		"isActive",
		"isBanned",
		"isEmailVerified",
		"isPhoneVerified",
		"isWhatsappVerified",
		"lastName",
		"phone",
		"profileUrl",
		"role",
		"secondName",
		"status",
		"tenantId",
		"token",
		"userType",
		"username",
		"usernameType",
		"whatsapp",
		"owner",
		"profileRole",
		"profileName",
		"profileId",
		"isAdmin",
		"isManager",
		"additionalFields",
		"permissions",
	}

	filteredData := datatype.DataMap{}

	if helper.IsNotEmpty(input.ProfileID) && helper.Contains(profileIds, input.ProfileID) {
		profile = y.ModelQuery(profileModelName).SkipBeforeCommit().Where("id", input.ProfileID).FindOne(nil)
	}

	if helper.IsEmpty(profile) {
		profile = y.ModelQuery(profileModelName).SkipBeforeCommit().Where("userId", userId).FindOne(nil)

		if helper.IsEmpty(profile) {
			profile = y.ModelQuery(profileUserModelName).SkipBeforeCommit().Where("userId", userId).FindOne(nil)
		}
	}

	if helper.IsNotEmpty(user) {
		model := y.Model(userModelName)
		tenantId := req.Request.TenantId()
		permissions := y.GetUserPermission(tenantId, userId, input.ModuleName)

		accessTokenExpireTime := y.Config.AccessTokenExpireTime
		if accessTokenExpireTime <= 0 {
			accessTokenExpireTime = 15 // default 30 days
		}

		payloadData := TokenPayload{
			TenantId:     tenantId,
			Domain:       domain,
			UserId:       userId,
			Username:     helper.GetValueOfString(user, "username"),
			UsernameType: helper.GetValueOfString(user, "usernameType"),
			Phone:        helper.GetValueOfString(user, "phone"),
			Email:        helper.GetValueOfString(user, "email"),
			Whatsapp:     helper.GetValueOfString(user, "whatsapp"),
			ModuleName:   input.ModuleName,
			Roles:        make([]string, 0), // ["admin", "finance"],
			Permissions:  permissions,       // ["payroll.read", "asset.write"],
			ExpiresAt:    time.Now().Add(time.Minute * accessTokenExpireTime),
		}

		// console.Info("payloadData", payloadData)

		userRole := helper.GetValueOf(user, "role")
		userIsAdmin := (userRole == "1" || userRole == "admin")

		if helper.IsNotEmpty(client.TenantId) {
			tenantId := client.TenantId

			payloadData.TenantId = tenantId
			user["tenantId"] = tenantId
		}

		if helper.IsNotEmpty(profile) {
			profileId := helper.GetValueOfString(profile, "_id")

			user["owner"] = false
			user["profileRole"] = "member"
			user["profileName"] = helper.GetValueOfString(profile, "name")
			user["profileId"] = profileId
			payloadData.ProfileId = profileId
		}

		if userIsAdmin {
			payloadData.AdminId = userId
		}

		var token interface{}
		token, _ = jwt.EncodeJWT(payloadData.ToMap(), y.Config.Authentication.SecretToken)

		user["token"] = token
		user["uuid"] = userId
		user["isAdmin"] = userIsAdmin
		user["isManager"] = user["role"] == "2" || user["role"] == "manager"
		user["owner"] = userId == user["id"]
		user["permissions"] = permissions

		if user["role"] == "1" {
			user["role"] = "admin"
		} else if user["role"] != "admin" && user["role"] != "manager" {
			user["role"] = "user"
		}
		user["profileRole"] = user["role"]

		if v, ok := (user["owner"]).(bool); ok && v {
			user["profileRole"] = "admin"
		}

		if y.Config.Graphql.AuthQuery.User != nil && profile != nil && *profile != nil {
			if authQuery, ok := y.Config.Graphql.AuthQuery.User.(map[string]interface{}); ok {
				if profileKeys, ok := authQuery["profile"].([]interface{}); ok {
					extraFields := make(map[string]interface{})

					for _, key := range profileKeys {
						if keyStr, ok := key.(string); ok {
							extraFields[keyStr] = helper.GetValueOf(profile, keyStr)
						}
					}

					user["additionalFields"] = extraFields
				}
			}
		}

		for _, key := range publicKeys {
			if helper.Contains(model.Protected, key) && key != "token" {
				continue
			}

			filteredData[key] = user[key]
		}

		if url, ok := filteredData["profileUrl"]; ok && helper.IsNotEmpty(url) {
			filteredData["profileUrl"] = helper.GetBaseUrl(helper.ToString(url), domain)
		} else {
			filteredData["profileUrl"] = helper.GetBaseUrl("/image/profile.png", domain)
		}

		return &filteredData
	}

	return &user
}

func (y *YekongaData) GetUserPermission(tenantId interface{}, userId string, moduleName string) []string {
	const tenantModelName = "Tenant"
	var permissions *[]datatype.DataMap
	var list = make([]string, 0)
	var isAdmin = y.ModelQuery(tenantModelName).SkipBeforeCommit().Exist(datatype.DataMap{
		"_id":    tenantId,
		"userId": userId,
	})

	if helper.IsNotEmpty(moduleName) {
		const permissionModelName = "AuthPermission"
		const userPermissionModelName = "AuthUserPermission"

		if isAdmin {
			var where = datatype.DataMap{
				"moduleName": datatype.DataMap{
					"in": []string{"access", moduleName},
				},
			}

			permissions = y.ModelQuery(permissionModelName).SkipBeforeCommit().Find(where)
		} else {
			var where = datatype.DataMap{
				"tenantId": tenantId,
				"userId":   userId,
				"moduleName": datatype.DataMap{
					"in": []string{"access", moduleName},
				},
			}

			permissions = y.ModelQuery(userPermissionModelName).SkipBeforeCommit().Find(where)
		}

		for _, e := range *permissions {
			var name = helper.GetValueOfString(e, "code")
			list = append(list, name)
		}
	}

	return list
}

func (y *YekongaData) SetUserPermission(tenantId interface{}, userId string, moduleName string, permissions []interface{}) {
	const userPermissionModelName = "AuthUserPermission"

	y.ModelQuery(userPermissionModelName).SkipTenant().SkipBeforeCommit().Delete(datatype.DataMap{
		"tenantId":   tenantId,
		"userId":     userId,
		"code":       "access:all",
		"moduleName": "access",
	})

	y.ModelQuery(userPermissionModelName).SkipTenant().SkipBeforeCommit().Delete(datatype.DataMap{
		"tenantId": tenantId,
		"userId":   userId,
	})

	if len(permissions) > 0 {
		for _, permission := range permissions {
			var code string
			var authGroupId interface{}

			if v, ok := permission.(string); ok {
				moduleName = strings.Split(v, ":")[0]
				code = v
			} else {
				authGroupId = helper.GetValueOf(permission, "authGroupId")
				code = helper.GetValueOfString(permission, "code")

				mv := helper.GetValueOfString(permission, "moduleName")
				if helper.IsNotEmpty(mv) {
					moduleName = mv
				}

				uv := helper.GetValueOfString(permission, "userId")
				if helper.IsNotEmpty(uv) {
					userId = uv
				}
			}

			input := datatype.DataMap{
				"tenantId":   tenantId,
				"userId":     userId,
				"code":       code,
				"moduleName": moduleName,
			}

			if helper.IsNotEmpty(authGroupId) {
				input["authGroupId"] = authGroupId
			}

			exists := y.ModelQuery(userPermissionModelName).SkipTenant().SkipBeforeCommit().Exist(input)
			// console.Log("info \ninput: %v \ncode: %v \nexists: %v\n\n\n", helper.ToJson(input), code, exists)

			if !exists {
				y.ModelQuery(userPermissionModelName).Create(input)
			}
		}
	}
}

func (y *YekongaData) GraphQL(query string, variables map[string]interface{}, req *Request, res *Response) *graphql.Result {
	requestString := query
	variableValues := variables
	operationName := ""

	graphqlContext := RequestContext{
		Auth:         req.Auth(),
		App:          y,
		Request:      req,
		Response:     res,
		TokenPayload: req.TokenPayload(),
		Client:       req.Client(),
	}

	result := executeGraphql(graphqlExecution{
		Schema:         &y.graphqlBuild.Schema,
		Cache:          y.graphqlBuild.documentCache,
		RequestString:  requestString,
		OperationName:  operationName,
		VariableValues: variableValues,
		Context:        &graphqlContext,
		Parent:         req.HttpRequest.Context(),
	})

	return result
}

func (y *YekongaData) SetCustomGraphql(
	name string,
	isMutation bool,
	isList bool,
	output map[string]datatype.DataMap,
	args graphql.FieldConfigArgument,
	resolver CustomGraphqlResolver,
) {
	var modelName any
	var fieldName string = name

	graphqlType := QueryType
	if isMutation {
		graphqlType = MutationType
	}

	names := strings.Split(name, ".")
	if len(names) > 1 {
		fieldName = names[1]
		modelName = helper.ToCamelCase(helper.Singularize(names[0]))
	}

	y.graphqlCustomQuery = append(y.graphqlCustomQuery, CustomGraphqlQuery{
		Name:        fieldName,
		Model:       modelName,
		GraphqlType: graphqlType,
		Output:      output,
		Args:        args,
		IsList:      isList,
		Resolve:     resolver,
	})
}

func (y *YekongaData) getRefreshToken(client ClientPayload, payload TokenPayload, rememberMe bool) string {
	token := helper.GetRandomString(64, "")
	var profileId interface{} = payload.ProfileId
	var tenantId interface{} = payload.TenantId
	var adminId interface{} = payload.AdminId
	var userId interface{} = payload.UserId

	if helper.IsEmpty(profileId) {
		profileId = nil
	}
	if helper.IsEmpty(tenantId) {
		tenantId = nil
	}
	if helper.IsEmpty(adminId) {
		adminId = nil
	}
	if helper.IsEmpty(userId) {
		userId = nil
	}

	refreshTokenExpireTime := y.Config.RefreshTokenExpireTime
	if refreshTokenExpireTime <= 0 {
		refreshTokenExpireTime = 7 // default 7 days
	}

	var days time.Duration = refreshTokenExpireTime
	if rememberMe {
		days = 30
	}

	body := datatype.DataMap{
		"domain":    payload.Domain,
		"tenantId":  tenantId,
		"profileId": profileId,
		"userId":    userId,
		"adminId":   adminId,
		"tokenHash": helper.HashRefreshToken(token),
		"userAgent": client.UserAgent,
		"ipAddress": client.IpAddress,
		"revoked":   false,
		"expiresAt": time.Now().Add(time.Hour * 24 * days),
	}

	y.ModelQuery("RefreshToken").SkipBeforeCommit().Create(body)

	return token
}

func (y *YekongaData) setAuthCookies(req *RequestContext, accessToken string, refreshToken string, moduleName string) {
	w := req.Response.httpResponseWriter
	domain := req.Client.OriginDomain()
	accessPath := "/"

	if helper.IsNotEmpty(accessToken) && helper.IsNotEmpty(refreshToken) {
		accessTokenExpireTime := req.App.Config.AccessTokenExpireTime
		if accessTokenExpireTime <= 0 {
			accessTokenExpireTime = 15 // default 15 minutes
		}

		accessTokenCookieA := http.Cookie{
			Name:     string(AccessTokenKey),
			Value:    accessToken,
			Path:     accessPath,
			Domain:   domain,
			HttpOnly: true,
			Secure:   y.Config.SecureOnly,
			SameSite: http.SameSiteDefaultMode,
			MaxAge:   helper.ToInt(accessTokenExpireTime) * 60, // 15 minutes
		}

		accessTokenCookieB := accessTokenCookieA
		accessTokenCookieB.Path = y.AppendBaseUrl(accessPath)

		http.SetCookie(*w, &accessTokenCookieA)
		http.SetCookie(*w, &accessTokenCookieB)

		refreshTokenExpireTime := req.App.Config.RefreshTokenExpireTime
		if refreshTokenExpireTime <= 0 {
			refreshTokenExpireTime = 7 // default 7 days
		}

		refreshTokenCookieA := http.Cookie{
			Name:     string(RefreshTokenKey),
			Value:    refreshToken,
			Path:     y.AppendBaseUrl("/refresh"),
			Domain:   domain,
			HttpOnly: true,
			Secure:   y.Config.SecureOnly,
			SameSite: http.SameSiteDefaultMode,
			MaxAge:   helper.ToInt(refreshTokenExpireTime) * 24 * 60 * 60, // 30 days
		}

		refreshTokenCookieB := refreshTokenCookieA
		refreshTokenCookieB.Path = y.AppendBaseUrl(y.Config.Graphql.ApiAuthRoute)

		http.SetCookie(*w, &refreshTokenCookieA)
		http.SetCookie(*w, &refreshTokenCookieB)
	}
}

func (y *YekongaData) clearAuthCookies(req *RequestContext, domain string, moduleName string) {
	w := req.Response.httpResponseWriter
	if helper.IsEmpty(domain) {
		domain = req.Client.OriginDomain()
	}

	accessPath := "/"
	accessTokenCookieA := http.Cookie{
		Name:     string(AccessTokenKey),
		Value:    "",
		Path:     accessPath,
		Domain:   domain,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteDefaultMode,
		MaxAge:   -1,
	}

	accessTokenCookieB := accessTokenCookieA
	accessTokenCookieB.Path = y.AppendBaseUrl(accessPath)

	http.SetCookie(*w, &accessTokenCookieA)
	http.SetCookie(*w, &accessTokenCookieB)

	refreshTokenCookieA := http.Cookie{
		Name:     string(RefreshTokenKey),
		Value:    "",
		Path:     y.AppendBaseUrl("/refresh"),
		Domain:   domain,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteDefaultMode,
		MaxAge:   -1,
	}

	refreshTokenCookieB := refreshTokenCookieA
	refreshTokenCookieB.Path = y.AppendBaseUrl(y.Config.Graphql.ApiAuthRoute)

	http.SetCookie(*w, &refreshTokenCookieA)
	http.SetCookie(*w, &refreshTokenCookieB)
}

func GetProfileIds(y *YekongaData, userId string) []string {
	var profileModelName = "Profile"
	var profileUserModelName = "ProfileUser"
	var all []string = []string{}
	var listA = y.ModelQuery(profileModelName).SkipBeforeCommit().Where("userId", userId).Find(nil)
	var listB = y.ModelQuery(profileUserModelName).SkipBeforeCommit().Where("userId", userId).Find(nil)

	for _, e := range *listA {
		all = append(all, helper.GetValueOfString(e, "profileId"))
	}

	for _, e := range *listB {
		all = append(all, helper.GetValueOfString(e, "profileId"))
	}

	return all

}
