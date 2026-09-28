package yekonga

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"google.golang.org/api/idtoken"

	"github.com/robertkonga/yekonga-server-go/datatype"
	"github.com/robertkonga/yekonga-server-go/helper"
	"github.com/robertkonga/yekonga-server-go/helper/console"
	"github.com/robertkonga/yekonga-server-go/plugins/graphql"
)

func (g *GraphqlAutoBuild) GetAuthQuery() *graphql.Object {
	var foreignKey string
	var targetKey string
	var name string = "User"

	var fields = make(graphql.Fields)
	fields["profile"] = &graphql.Field{
		Type: UserProfileType,
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			var input = helper.ToDataMap(g.getInputData(p.Args))
			var model = g.yekonga.ModelQuery(name)

			g.setModelParams(model, &p, foreignKey, targetKey, false)
			user := model.FindOne(input)

			return user, nil
		},
	}
	fields["profile"] = _profile(g)
	fields["refreshToken"] = _refreshToken(g)
	fields["tenantAvailability"] = _tenantAvailability(g)

	var queryType = graphql.NewObject(
		graphql.ObjectConfig{
			Name:   "Query",
			Fields: fields,
		})

	return queryType
}

func (g *GraphqlAutoBuild) GetAuthMutation() *graphql.Object {
	var fields = make(graphql.Fields)

	fields["otp"] = _otp(g)
	fields["login"] = _login(g)
	fields["register"] = _registration(g)
	fields["socialLogin"] = _socialLogin(g)
	fields["contactOTP"] = _contactOTP(g)
	fields["contactVerify"] = _contactVerify(g)
	fields["resetPassword"] = _resetPassword(g)
	fields["confirmToken"] = _confirmToken(g)
	fields["changePassword"] = _changePassword(g)
	fields["switchAccount"] = _switchAccount(g)

	var mutationType = graphql.NewObject(
		graphql.ObjectConfig{
			Name:   "Mutation",
			Fields: fields,
		})

	return mutationType
}

// otp ( input: OtpInput!, type: UsernameIdentifier ): ActionResponse,
func _otp(g *GraphqlAutoBuild) *graphql.Field {
	// var foreignKey string
	// var targetKey string
	// var tenantModelName = "Tenant"
	var tenantUserModelName = "TenantUser"

	return &graphql.Field{
		Type: ActionResponseType,
		Args: graphql.FieldConfigArgument{
			"input": &graphql.ArgumentConfig{
				Type: OtpInput,
			},
			"type": &graphql.ArgumentConfig{
				Type: UsernameIdentifierEnum,
			},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			req, _ := p.Context.Value(RequestContextKey).(*RequestContext)
			tenantConfig := req.Request.Tenant()
			var result = ActionResponse{
				Message: "You have no access",
			}
			var user datatype.DataMap
			var data = helper.ToDataMap(g.getInputData(p.Args))
			var tenantId = req.Request.TenantId()
			var tenant = req.Request.Tenant()
			// console.Log("tenant", tenant)

			var username = helper.GetValueOfString(data, "username")
			var usernameType = helper.GetValueOfString(data, "usernameType")
			var userId = ""
			if helper.IsPhone(username) {
				username = helper.PhoneFormat(username)
			}

			if helper.IsNotEmpty(tenantId) {
				if helper.IsNotEmpty(username) {
					u := g.yekonga.SetOTPVerification(username, usernameType, tenantConfig.PublicCanRegister, "login", req.Request)

					if helper.IsNotEmpty(u) {
						user = *u
						userId = helper.GetValueOfString(user, "userId")
					}
				}

				// console.Log("user", user)
				if helper.IsNotEmpty(userId) {
					tenantUser := (userId == tenant.UserId)

					if !tenantUser {
						tenantUser = req.App.ModelQuery(tenantUserModelName).SkipTenant().SkipBeforeCommit().Exist(datatype.DataMap{
							"tenantId": tenantId,
							"userId":   userId,
						})

						if !tenantUser && !tenantConfig.PublicCanRegister {
							return nil, errors.New("User does not exist")
						}
					}
				} else if !tenantConfig.PublicCanRegister {
					return nil, errors.New("User does not exist at all")
				}
			} else {
				triggerResult, _ := g.yekonga.authTriggerCallback(BeforeOtpTriggerAction, req, &QueryContext{
					Data:  data,
					Input: data,
				})

				if v, ok := triggerResult.(bool); ok && !v {
					return nil, errors.New("Rejected by BeforeOtpTriggerAction")
				}

				if helper.IsNotEmpty(username) {
					user = *g.yekonga.SetOTPVerification(username, usernameType, true, "login", req.Request)
				}
				userId = helper.GetValueOfString(user, "userId")
			}

			g.yekonga.RecordLoginAttempt("otp", p.Context, AttemptData{
				UserID:   userId,
				Username: username,
			})

			if helper.IsNotEmpty(user) {
				result.Status = true
				result.Message = "Success"
			}

			g.yekonga.authTriggerCallback(AfterOtpTriggerAction, req, &QueryContext{
				Data:  result,
				Input: data,
			})

			return result.ToMap(), nil
		},
	}
}

func _login(g *GraphqlAutoBuild) *graphql.Field {
	// var foreignKey string
	// var targetKey string
	// var name string = "User"
	var targetType *graphql.Object

	if g.yekonga.Config.SecureAuthentication {
		targetType = CredentialTokenType
	} else {
		targetType = UserProfileType
	}

	return &graphql.Field{
		Type: targetType,
		Args: graphql.FieldConfigArgument{
			"input": &graphql.ArgumentConfig{
				Type: LoginInput,
			},
			"moduleName": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
			"type": &graphql.ArgumentConfig{
				Type: UsernameIdentifierEnum,
			},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			req, _ := p.Context.Value(RequestContextKey).(*RequestContext)
			var input = helper.ToDataMap(g.getInputData(p.Args))
			var user datatype.DataMap
			var username = helper.GetValueOfString(input, "username")
			var usernameType = helper.GetValueOfString(input, "usernameType")
			var password = helper.GetValueOfString(input, "password")
			var loginType = helper.GetValueOfString(input, "type")
			var rememberMe = helper.GetValueOfBoolean(input, "rememberMe")
			var moduleName = helper.GetValueOfString(input, "moduleName")

			cookieEnabled := ""
			cookie, err := req.Request.HttpRequest.Cookie(COOKIE_ENABLED_KEY)
			if err == nil {
				cookieEnabled = cookie.Value
			}

			if helper.IsPhone(username) {
				username = helper.PhoneFormat(username)
			}
			if helper.IsNotEmpty(username) {
				// user = g.yekonga.GetUser(username, false)
				u := g.yekonga.OTPVerification(username, password, usernameType, true, req.Request)
				if helper.IsNotEmpty(u) {
					user = *u
				}
			}

			triggerResult, _ := g.yekonga.authTriggerCallback(BeforeLoginTriggerAction, req, &QueryContext{
				Data:  user,
				Input: input,
			})

			if v, ok := triggerResult.(bool); ok && !v {
				return nil, errors.New("Rejected by before BeforeLoginTriggerAction")
			} else if v, ok := triggerResult.(datatype.DataMap); ok {
				user = v
			}

			if helper.IsNotEmpty(user) {
				attemptData := AttemptData{
					Username:     username,
					UsernameType: usernameType,
					Password:     password,
					LoginType:    loginType,
					RememberMe:   rememberMe,
					ModuleName:   moduleName,
				}

				u, e := g.yekonga.AttemptLogin(p.Context, attemptData)

				if helper.IsNotEmpty(u) {
					user = *u
					userId := helper.GetValueOfString(user, "id")

					attemptData.UserID = userId
					attemptData.ProfileID = helper.GetValueOfString(user, "profileId")
					// console.Info(user)

					g.yekonga.RecordLoginAttempt("success", p.Context, attemptData)
					g.yekonga.authTriggerCallback(AfterLoginTriggerAction, req, &QueryContext{
						Data:  user,
						Input: input,
					})

					if req.Request != nil {
						accessToken := helper.GetValueOfString(user, "token")
						profileId := helper.GetValueOfString(user, "profileId")
						tenantId := helper.GetValueOfString(user, "tenantId")
						adminId := helper.GetValueOfString(user, "adminId")
						permissions := g.yekonga.GetUserPermission(tenantId, userId, moduleName)
						accessTokenExpireTime := req.App.Config.AccessTokenExpireTime
						if accessTokenExpireTime <= 0 {
							accessTokenExpireTime = 15 // default 15 minutes
						}

						refreshTokenData := TokenPayload{
							Domain:       req.Client.OriginDomain(),
							TenantId:     tenantId,
							ProfileId:    profileId,
							UserId:       userId,
							AdminId:      adminId,
							Username:     helper.GetValueOfString(user, "username"),
							UsernameType: helper.GetValueOfString(user, "usernameType"),
							Phone:        helper.GetValueOfString(user, "phone"),
							Email:        helper.GetValueOfString(user, "email"),
							Whatsapp:     helper.GetValueOfString(user, "whatsapp"),
							ModuleName:   moduleName,
							Roles:        make([]string, 0), // ["admin", "finance"],
							Permissions:  permissions,       // ["payroll.read", "asset.write"],
							ExpiresAt:    helper.GetTimestamp(nil).Add(time.Minute * accessTokenExpireTime),
						}

						// console.Info("refreshTokenData", refreshTokenData)
						refreshToken := g.yekonga.getRefreshToken(*req.Client, refreshTokenData, rememberMe)

						if helper.IsEmpty(cookieEnabled) {
							user[helper.ToVariable(string(AccessTokenKey))] = accessToken
							user[helper.ToVariable(string(RefreshTokenKey))] = refreshToken
						} else {
							user[helper.ToVariable(string(AccessTokenKey))] = "Cookie is set"
							user[helper.ToVariable(string(RefreshTokenKey))] = "Cookie is set"
						}

						g.yekonga.setAuthCookies(req, accessToken, refreshToken, moduleName)
					}

					return user, nil
				} else if e != nil {
					g.yekonga.RecordLoginAttempt("fail", p.Context, AttemptData{
						Username:     username,
						UsernameType: usernameType,
						LoginType:    loginType,
					})

					return nil, e
				}
			}

			g.yekonga.RecordLoginAttempt("fail", p.Context, AttemptData{
				Username:     username,
				UsernameType: usernameType,
				LoginType:    loginType,
			})

			return nil, errors.New("Wrong credential")
		},
	}
}

func _verifyOtp(g *GraphqlAutoBuild) *graphql.Field {
	// var foreignKey string
	// var targetKey string
	// var name string = "User"
	var targetType *graphql.Object

	if g.yekonga.Config.SecureAuthentication {
		targetType = CredentialTokenType
	} else {
		targetType = UserProfileType
	}

	return &graphql.Field{
		Type: targetType,
		Args: graphql.FieldConfigArgument{
			"input": &graphql.ArgumentConfig{
				Type: LoginInput,
			},
			"moduleName": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
			"type": &graphql.ArgumentConfig{
				Type: UsernameIdentifierEnum,
			},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			req, _ := p.Context.Value(RequestContextKey).(*RequestContext)
			var input = helper.ToDataMap(g.getInputData(p.Args))
			var user datatype.DataMap
			var username = helper.GetValueOfString(input, "username")
			var usernameType = helper.GetValueOfString(input, "usernameType")
			var password = helper.GetValueOfString(input, "password")
			var loginType = helper.GetValueOfString(input, "type")
			var moduleName = helper.GetValueOfString(input, "moduleName")
			var rememberMe = helper.GetValueOfBoolean(input, "rememberMe")

			cookieEnabled := ""
			cookie, err := req.Request.HttpRequest.Cookie(COOKIE_ENABLED_KEY)
			if err == nil {
				cookieEnabled = cookie.Value
			}

			if helper.IsPhone(username) {
				username = helper.PhoneFormat(username)
			}
			if helper.IsNotEmpty(username) {
				// user = g.yekonga.GetUser(username, false)
				user = *g.yekonga.OTPVerification(username, password, usernameType, false, req.Request)
			}

			triggerResult, _ := g.yekonga.authTriggerCallback(BeforeLoginTriggerAction, req, &QueryContext{
				Data:  user,
				Input: input,
			})

			if v, ok := triggerResult.(bool); ok && !v {
				return nil, errors.New("Rejected by before BeforeLoginTriggerAction")
			} else if v, ok := triggerResult.(datatype.DataMap); ok {
				user = v
			}

			if helper.IsNotEmpty(user) {
				attemptData := AttemptData{
					Username:     username,
					UsernameType: usernameType,
					Password:     password,
					LoginType:    loginType,
					RememberMe:   rememberMe,
				}

				u, e := g.yekonga.AttemptLogin(p.Context, attemptData)

				if helper.IsNotEmpty(u) {
					user = *u
					userId := helper.GetValueOfString(user, "id")

					attemptData.UserID = userId
					attemptData.ProfileID = helper.GetValueOfString(user, "profileId")

					g.yekonga.RecordLoginAttempt("success", p.Context, attemptData)
					g.yekonga.authTriggerCallback(AfterLoginTriggerAction, req, &QueryContext{
						Data:  user,
						Input: input,
					})

					if req.Request != nil {
						accessToken := helper.GetValueOfString(user, "token")
						profileId := helper.GetValueOfString(user, "profileId")
						tenantId := helper.GetValueOfString(user, "tenantId")
						adminId := helper.GetValueOfString(user, "adminId")
						permissions := g.yekonga.GetUserPermission(tenantId, userId, moduleName)
						accessTokenExpireTime := req.App.Config.AccessTokenExpireTime
						if accessTokenExpireTime <= 0 {
							accessTokenExpireTime = 15 // default 15 minutes
						}

						refreshTokenData := TokenPayload{
							Domain:       req.Client.OriginDomain(),
							TenantId:     tenantId,
							ProfileId:    profileId,
							UserId:       userId,
							AdminId:      adminId,
							Username:     helper.GetValueOfString(user, "username"),
							UsernameType: helper.GetValueOfString(user, "usernameType"),
							Phone:        helper.GetValueOfString(user, "phone"),
							Email:        helper.GetValueOfString(user, "email"),
							Whatsapp:     helper.GetValueOfString(user, "whatsapp"),
							ModuleName:   moduleName,
							Roles:        make([]string, 0), // ["admin", "finance"],
							Permissions:  permissions,       // ["payroll.read", "asset.write"],
							ExpiresAt:    helper.GetTimestamp(nil).Add(time.Minute * accessTokenExpireTime),
						}

						// console.Info("refreshTokenData", refreshTokenData)

						refreshToken := g.yekonga.getRefreshToken(*req.Client, refreshTokenData, rememberMe)

						if helper.IsEmpty(cookieEnabled) {
							user[helper.ToVariable(string(AccessTokenKey))] = accessToken
							user[helper.ToVariable(string(RefreshTokenKey))] = refreshToken
						} else {
							user[helper.ToVariable(string(AccessTokenKey))] = "Cookie is set"
							user[helper.ToVariable(string(RefreshTokenKey))] = "Cookie is set"
						}

						g.yekonga.setAuthCookies(req, accessToken, refreshToken, moduleName)
					}

					return user, nil
				} else if e != nil {
					return nil, e
				}
			}

			return nil, errors.New("Wrong credential")
		},
	}
}

func _refreshToken(g *GraphqlAutoBuild) *graphql.Field {
	// var foreignKey string
	// var targetKey string
	// var name string = "User"
	var targetType *graphql.Object

	if g.yekonga.Config.SecureAuthentication {
		targetType = CredentialTokenType
	} else {
		targetType = UserProfileType
	}

	return &graphql.Field{
		Type: targetType,
		Args: graphql.FieldConfigArgument{
			"refreshToken": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
			"moduleName": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			req, _ := p.Context.Value(RequestContextKey).(*RequestContext)
			refreshToken := helper.GetValueOfString(p.Args, "refreshToken")
			moduleName := helper.GetValueOfString(p.Args, "moduleName")

			if g.yekonga.Config.SecureAuthentication {
				result, status := req.App.refreshTokenProcess(req.Request, req.Response, refreshToken, moduleName)

				if status == http.StatusOK {
					return result, nil
				}

				if err, ok := result["error"]; ok {
					return nil, errors.New(helper.ToString(err))
				}
				return nil, nil
			}

			return nil, nil
		},
	}
}

// socialLogin ( input: SocialLoginInput! ): Profile,
func _socialLogin(g *GraphqlAutoBuild) *graphql.Field {
	var foreignKey string
	var targetKey string
	var name string = "User"

	return &graphql.Field{
		Type: UserProfileType,
		Args: graphql.FieldConfigArgument{
			"input": &graphql.ArgumentConfig{
				Type: SocialLoginInput,
			},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			var input map[string]interface{} = helper.ToDataMap(g.getInputData(p.Args))
			var model = g.yekonga.ModelQuery(name)

			googleClientID := g.yekonga.Config.GoogleClientId
			credential := helper.GetValueOfString(input, "credential")

			payload, err := idtoken.Validate(context.Background(), credential, googleClientID)
			if err != nil {
				return nil, err
			}

			// 'sub' is the unique Google ID (never changes)
			googleUserID := payload.Subject
			email := payload.Claims["email"].(string)
			name := payload.Claims["name"].(string)
			firstName := payload.Claims["given_name"].(string)
			secondName := ""
			lastName := payload.Claims["family_name"].(string)
			profileUrl := payload.Claims["picture"].(string)
			username := email
			usernameType := "email"

			if helper.IsNotEmpty(firstName) {
				list := strings.SplitN(firstName, " ", 2)

				if helper.IsEmpty(firstName) {
					firstName = list[0]
				}
				if helper.IsEmpty(secondName) && len(list) > 1 {
					secondName = list[1]
				}
				if helper.IsEmpty(lastName) {
					lastName = strings.TrimSpace(strings.TrimPrefix(name, firstName))
				}
			}

			var data map[string]interface{} = make(map[string]interface{})
			data["googleUserID"] = googleUserID
			data["email"] = email
			data["name"] = name
			data["firstName"] = firstName
			data["lastName"] = lastName
			data["profileUrl"] = profileUrl
			data["username"] = username
			data["usernameType"] = usernameType

			g.setModelParams(model, &p, foreignKey, targetKey, false)
			user := model.Create(data)

			return user, nil
		},
	}
}

// contactOTP ( input: ConcatOtpInput!, type: UsernameIdentifier! ): ActionResponse,
func _contactOTP(g *GraphqlAutoBuild) *graphql.Field {
	var foreignKey string
	var targetKey string
	var name string = "User"

	return &graphql.Field{
		Type: ActionResponseType,
		Args: graphql.FieldConfigArgument{
			"input": &graphql.ArgumentConfig{
				Type: ConcatOtpInput,
			},
			"type": &graphql.ArgumentConfig{
				Type: UsernameIdentifierEnum,
			},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			var input = helper.ToDataMap(g.getInputData(p.Args))
			// var type = g.getParamValue(p.Args, "type")
			var model = g.yekonga.ModelQuery(name)

			g.setModelParams(model, &p, foreignKey, targetKey, false)
			user := model.FindOne(input)

			return user, nil
		},
	}
}

// contactVerify ( input: ContactVerifyInput!, type: UsernameIdentifier! ): ActionResponse,
func _contactVerify(g *GraphqlAutoBuild) *graphql.Field {
	var foreignKey string
	var targetKey string
	var name string = "User"

	return &graphql.Field{
		Type: ActionResponseType,
		Args: graphql.FieldConfigArgument{
			"input": &graphql.ArgumentConfig{
				Type: ContactVerifyInput,
			},
			"type": &graphql.ArgumentConfig{
				Type: UsernameIdentifierEnum,
			},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			var input = helper.ToDataMap(g.getInputData(p.Args))
			// var type = g.getParamValue(p.Args, "type")
			var model = g.yekonga.ModelQuery(name)

			g.setModelParams(model, &p, foreignKey, targetKey, false)
			user := model.FindOne(input)

			return user, nil
		},
	}
}

// registration ( input: RegistrationInput! ): Profile,
func _registration(g *GraphqlAutoBuild) *graphql.Field {
	var foreignKey string
	var targetKey string
	var userModelName string = "User"
	var tenantModelName string = "Tenant"

	return &graphql.Field{
		Type: ActionResponseType,
		Args: graphql.FieldConfigArgument{
			"input": &graphql.ArgumentConfig{
				Type: RegistrationInput,
			},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			var input = helper.ToDataMap(g.getInputData(p.Args))
			var ctx, _ = p.Context.Value(RequestContextKey).(*RequestContext)
			var auth = ctx.Auth

			if helper.IsNotEmpty(auth) {
				var model = g.yekonga.ModelQuery(tenantModelName)
				g.setModelParams(model, &p, foreignKey, targetKey, false)
				input["userId"] = auth.ID
				input["name"] = input["organization"]

				if helper.IsEmpty(input["language"]) {
					input["language"] = "en"
				}
				if helper.IsEmpty(input["language"]) {
					input["status"] = "active"
				}

				triggerResult, _ := g.yekonga.authTriggerCallback(BeforeRegisterTriggerAction, ctx, &QueryContext{
					Data:  input,
					Input: input,
				})

				if v, ok := triggerResult.(bool); ok && !v {
					return nil, errors.New("Rejected by before BeforeRegisterTriggerAction")
				} else if v, ok := triggerResult.(datatype.DataMap); ok {
					input = v
				}

				tenant := model.Create(input)

				triggerResult, _ = g.yekonga.authTriggerCallback(AfterRegisterTriggerAction, ctx, &QueryContext{
					Data:  tenant,
					Input: input,
				})

				if v, ok := triggerResult.(datatype.DataMap); ok {
					tenant = v
				}

				if helper.IsNotEmpty(tenant) {
					g.yekonga.ModelQuery(userModelName).Update(datatype.DataMap{
						"firstName": input["firstName"],
						"lastName":  input["lastName"],
					}, datatype.DataMap{
						"id": auth.ID,
					})
				}

				return datatype.DataMap{
					"status":  true,
					"message": "SUCCESS",
					"data": datatype.DataMap{
						"id":          helper.GetValueOf(tenant, "_id"),
						"name":        helper.GetValueOf(tenant, "name"),
						"description": helper.GetValueOf(tenant, "description"),
						"logoUrl":     helper.GetValueOf(tenant, "logoUrl"),
						"email":       helper.GetValueOf(tenant, "email"),
						"phone":       helper.GetValueOf(tenant, "phone"),
						"whatsapp":    helper.GetValueOf(tenant, "whatsapp"),
						"type":        helper.GetValueOf(tenant, "type"),
						"domain":      helper.GetValueOf(tenant, "domain"),
						"subdomain":   helper.GetValueOf(tenant, "subdomain"),
						"status":      helper.GetValueOf(tenant, "status"),
					},
				}, nil
			}

			return nil, errors.New("Not authorized")

		},
	}
}

func _tenantAvailability(g *GraphqlAutoBuild) *graphql.Field {
	var tenantModelName string = "Tenant"

	return &graphql.Field{
		Type: graphql.Boolean,
		Args: graphql.FieldConfigArgument{
			"id": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
			"domain": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
			"subdomain": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
			"customDomain": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
			"customSubdomain": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			var id = helper.GetValueOfString(p.Args, "id")
			var domain = helper.GetValueOfString(p.Args, "domain")
			var subdomain = helper.GetValueOfString(p.Args, "subdomain")
			var customDomain = helper.GetValueOfString(p.Args, "customDomain")
			var customSubdomain = helper.GetValueOfString(p.Args, "customSubdomain")
			var model = g.yekonga.ModelQuery(tenantModelName).SkipBeforeCommit()
			var validQuery = false
			var tenant *datatype.DataMap

			var where = []datatype.DataMap{}
			if helper.IsNotEmpty(id) {
				model.Where("id", id)
				validQuery = true
			}

			if helper.IsNotEmpty(domain) {
				where = append(where, datatype.DataMap{
					"domain": datatype.DataMap{"equalTo": domain},
				})

				validQuery = true
			}

			if helper.IsNotEmpty(subdomain) {
				where = append(where, datatype.DataMap{
					"subdomain": datatype.DataMap{"equalTo": subdomain},
				})

				validQuery = true
			}

			if helper.IsNotEmpty(customDomain) {
				where = append(where, datatype.DataMap{
					"customDomain": datatype.DataMap{"equalTo": customDomain},
				})

				validQuery = true
			}

			if helper.IsNotEmpty(customSubdomain) {
				where = append(where, datatype.DataMap{
					"customSubdomain": datatype.DataMap{"equalTo": customSubdomain},
				})

				validQuery = true
			}

			if validQuery {
				model.WhereAll(datatype.DataMap{
					"OR": where,
				})
				tenant = model.FindOne(nil)
			}

			if helper.IsNotEmpty(tenant) {
				return true, nil
			}

			return false, nil
		},
	}
}

// resetPassword ( input: ResetPasswordInput! ): ActionResponse,
func _resetPassword(g *GraphqlAutoBuild) *graphql.Field {
	var foreignKey string
	var targetKey string
	var name string = "User"

	return &graphql.Field{
		Type: ActionResponseType,
		Args: graphql.FieldConfigArgument{
			"input": &graphql.ArgumentConfig{
				Type: ResetPasswordInput,
			},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			var data = helper.ToDataMap(g.getInputData(p.Args))
			var model = g.yekonga.ModelQuery(name)

			g.setModelParams(model, &p, foreignKey, targetKey, false)
			user := model.FindOne(data)

			return user, nil
		},
	}
}

// confirmToken ( input: ConfirmTokenInput! ): ConfirmTokenResponse,
func _confirmToken(g *GraphqlAutoBuild) *graphql.Field {
	var foreignKey string
	var targetKey string
	var name string = "User"

	return &graphql.Field{
		Type: ConfirmTokenResponseType,
		Args: graphql.FieldConfigArgument{
			"input": &graphql.ArgumentConfig{
				Type: ConfirmTokenInput,
			},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			var input = helper.ToDataMap(g.getInputData(p.Args))
			var model = g.yekonga.ModelQuery(name)

			g.setModelParams(model, &p, foreignKey, targetKey, false)
			user := model.FindOne(input)

			return user, nil
		},
	}
}

// changePassword ( input: ChangePasswordInput! ): ActionResponse,
func _changePassword(g *GraphqlAutoBuild) *graphql.Field {
	var foreignKey string
	var targetKey string
	var name string = "User"

	return &graphql.Field{
		Type: ActionResponseType,
		Args: graphql.FieldConfigArgument{
			"input": &graphql.ArgumentConfig{
				Type: ChangePasswordInput,
			},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			var input = helper.ToDataMap(g.getInputData(p.Args))
			var model = g.yekonga.ModelQuery(name)

			g.setModelParams(model, &p, foreignKey, targetKey, false)
			user := model.FindOne(input)

			return user, nil
		},
	}
}

// profile ( input: ProfileInput! ): Profile,
func _profile(g *GraphqlAutoBuild) *graphql.Field {

	return &graphql.Field{
		Type: UserProfileType,
		Args: graphql.FieldConfigArgument{
			"input": &graphql.ArgumentConfig{
				Type: UserProfileInput,
			},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			var ctx, _ = p.Context.Value(RequestContextKey).(*RequestContext)
			var auth = ctx.Auth

			if helper.IsNotEmpty(auth) {
				user := ctx.App.GetLoginData(ctx, &LoginData{
					UserID:    auth.ID,
					ProfileID: auth.ProfileID,
				})

				return user, nil
			}

			return nil, errors.New("Not authorized")
		},
	}
}

// switchAccount ( profileId: String, userId: String, token: String ): Profile,
func _switchAccount(g *GraphqlAutoBuild) *graphql.Field {
	var foreignKey string
	var targetKey string
	var name string = "User"

	return &graphql.Field{
		Type: UserProfileType,
		Args: graphql.FieldConfigArgument{
			"profileId": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
			"userId": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
			"token": &graphql.ArgumentConfig{
				Type: graphql.String,
			},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			var data = helper.ToDataMap(g.getInputData(p.Args))
			var profileId = g.getParamValue(p.Args, "profileId")
			var userId = g.getParamValue(p.Args, "userId")
			var token = g.getParamValue(p.Args, "token")

			console.Success("_switchAccount.token", token)
			var model = g.yekonga.ModelQuery(name)

			g.setModelParams(model, &p, foreignKey, targetKey, false)
			model.Where("profileId", profileId)
			model.Where("userId", userId)

			user := model.FindOne(data)

			return user, nil
		},
	}
}
