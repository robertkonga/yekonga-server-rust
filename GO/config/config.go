package config

import "time"

type DatabaseType string

const (
	DBTypeMongodb DatabaseType = "mongodb"
	DBTypeSql     DatabaseType = "sql"
	DBTypeMysql   DatabaseType = "mysql"
	DBTypeLocal   DatabaseType = "local"
)

type GatewayProvider string

const (
	ProviderBeem    GatewayProvider = "beem"
	ProviderInfobip GatewayProvider = "infobip"
	ProviderAlibaba GatewayProvider = "alibaba"
)

type SMSGatewayConfig struct { // SMS gateway configuration
	Provider  GatewayProvider `json:"provider"`  // SMS provider (e.g., beem, infobip)
	BaseURL   string          `json:"baseURL"`   // Base URL for the SMS gateway API
	Sender    string          `json:"sender"`    // Sender ID or name for SMS messages
	APIKey    string          `json:"apiKey"`    // API key for authentication
	SecretKey string          `json:"secretKey"` // Secret key for authentication
	Username  string          `json:"username"`  // Username for authentication
	Password  string          `json:"password"`  // Password for authentication
}

type WhatsappGatewayConfig struct { // WhatsApp gateway configuration
	Provider GatewayProvider `json:"provider"` // WhatsApp provider (e.g., infobip)
	Sender   string          `json:"sender"`   // Sender ID or name for WhatsApp messages
	Sandbox  string          `json:"sandbox"`  // Sandbox environment indicator
	BaseURL  string          `json:"baseURL"`  // Base URL for the WhatsApp gateway API
	APIKey   string          `json:"apiKey"`   // API key for authentication
}

type PaymentProviderConfig struct { // One payment gateway (see gateway/payment Key* constants for credential names)
	Provider    string            `json:"provider"`    // Gateway name (flutterwave, paypal, pesapal, selcom, clickpesa, azampay, stripe, twocheckout)
	Sandbox     bool              `json:"sandbox"`     // Use the gateway's test environment
	BaseURL     string            `json:"baseURL"`     // Overrides the gateway's built-in endpoint
	WebhookURL  string            `json:"webhookURL"`  // Notification URL sent to the gateway; empty = derived from the request host and webhookRoute
	Credentials map[string]string `json:"credentials"` // Gateway credentials keyed by payment.Key* names (secret_key, client_id, ...)
}

type PaymentGatewayConfig struct { // Payment gateway configuration
	WebhookRoute string                  `json:"webhookRoute"` // Route prefix for provider notifications, served as <webhookRoute>/:provider; empty = /payment/webhook
	Providers    []PaymentProviderConfig `json:"providers"`    // Enabled payment gateways
}

// SMTPConfig holds SMTP configuration
type SMTPConfig struct {
	Service  string `json:"service"`  // SMTP service provider
	Host     string `json:"host"`     // SMTP host address
	Port     int    `json:"port"`     // SMTP port
	Secure   bool   `json:"secure"`   // Enable secure connection (SSL/TLS)
	From     string `json:"from"`     // Sender email address
	Domain   string `json:"domain"`   // Sending domain
	Username string `json:"username"` // SMTP username
	Password string `json:"password"` // SMTP password
	APIKey   string `json:"apiKey"`   // API key for transactional email services
}

type Branding struct { // Branding configuration for the application
	LogoUrl             string `json:"logoUrl"`             // URL to the application logo
	FaviconUrl          string `json:"faviconUrl"`          // URL to the favicon
	PrimaryColor        string `json:"primaryColor"`        // Primary color for the application UI
	SecondaryColor      string `json:"secondaryColor"`      // Secondary color for the application UI
	DarkBackgroundColor string `json:"darkBackgroundColor"` // Background color for dark mode
}

type YekongaConfig struct {
	AppName                 string        `json:"appName"`         // Name of the application
	Version                 string        `json:"version"`         // Version of the application
	Description             string        `json:"description"`     // Description of the application
	AppKey                  string        `json:"appKey"`          // Key for the application
	MasterKey               string        `json:"masterKey"`       // Master key for the application
	EnableAppKey            bool          `json:"enableAppKey"`    // Enable or disable app key usage
	ConnectionID            string        `json:"connectionID"`    // Connection ID
	UserIdentifiers         []string      `json:"userIdentifiers"` // List of user identifiers
	Domain                  string        `json:"domain"`          // Application domain
	Protocol                string        `json:"protocol"`        // Protocol (e.g., http, https)
	DomainAlias             []string      `json:"domainAlias"`     // List of domain aliases
	Address                 string        `json:"address"`         // Application address
	BaseUrl                 string        `json:"baseURL"`
	RestApiEnabled          bool          `json:"restApiEnabled"`          // Base URL of the application
	RestApi                 string        `json:"restAPI"`                 // REST API endpoint
	RestAuthApi             string        `json:"restAuthAPI"`             // REST authentication API endpoint
	TokenKey                string        `json:"tokenKey"`                // Key for generating tokens
	PdfInstances            int           `json:"pdfInstances"`            // Number of PDF instances
	AccessTokenExpireTime   time.Duration `json:"accessTokenExpireTime"`   // Access token expiration time in minutes
	RefreshTokenExpireTime  time.Duration `json:"refreshTokenExpireTime"`  // Refresh token expiration time in minutes
	SecureOnly              bool          `json:"secureOnly"`              // Enforce secure connections only
	Debug                   bool          `json:"debug"`                   // Enable or disable debug mode
	Cors                    bool          `json:"cors"`                    // Enable or disable CORS
	ResetOTP                bool          `json:"resetOTP"`                // Enable or disable OTP reset
	Environment             string        `json:"environment"`             // Application environment (e.g., development, production)
	HasTenant               bool          `json:"hasTenant"`               // Enable multi-tenancy
	TenantOnly              bool          `json:"tenantOnly"`              // Restrict access to tenants only
	HasTenantBilling        bool          `json:"hasTenantBilling"`        // Enable tenant billing features
	HasPaymentModule        bool          `json:"hasPaymentModule"`        // Enable the Payment models without tenant billing (tenant billing already includes them)
	HasTenantCatch          bool          `json:"hasTenantCatch"`          // Enable tenant catch for domain-based tenant resolution
	SecureAuthentication    bool          `json:"secureAuthentication"`    // Enable or disable secure authentication
	IsAuthorizationServer   bool          `json:"isAuthorizationServer"`   // Designate as an authorization server
	AuthorizeTenantUserOnly bool          `json:"authorizeTenantUserOnly"` // Restrict authorization to tenant users only
	AuthorizedOnly          bool          `json:"authorizedOnly"`          // Require authorization for all requests
	HasCronjob              bool          `json:"hasCronjob"`              // Require authorization for all requests
	RegisterUserOnOtp       bool          `json:"registerUserOnOtp"`       // Register user automatically on OTP verification
	SendOtpToSmsAndWhatsapp bool          `json:"sendOtpToSmsAndWhatsapp"` // Send OTP via SMS and WhatsApp
	EndToEndEncryption      bool          `json:"endToEndEncryption"`      // Enable end-to-end encryption
	AuthPlaygroundEnable    bool          `json:"authPlaygroundEnable"`    // Enable authentication playground
	ApiPlaygroundEnable     bool          `json:"apiPlaygroundEnable"`     // Enable API playground
	EnableDashboard         bool          `json:"enableDashboard"`         // Enable admin dashboard
	AllowCreateFrontend     bool          `json:"allowCreateFrontend"`     // Allow frontend creation
	NamingConvention        string        `json:"namingConvention"`        // Naming convention for database tables and fields
	ColumnNamingConvention  string        `json:"columnNamingConvention"`  // Naming convention for database columns
	NamingConventionOptions []string      `json:"namingConventionOptions"` // Options for naming conventions
	Public                  []string      `json:"public"`                  // List of public routes/endpoints
	Cloud                   string        `json:"cloud"`                   // Cloud provider configuration
	LogFile                 string        `json:"logFile"`                 // Path to the log file
	IndexTemplate           string        `json:"indexTemplate"`           // Path to the index HTML template
	EmailTemplate           string        `json:"emailTemplate"`           // Path to the email HTML template
	GoogleApiKey            string        `json:"googleApiKey"`            // Google API key
	GoogleApiKeyAlt         string        `json:"googleApiKeyAlt"`         // Alternative Google API key
	GoogleClientId          string        `json:"googleClientId"`          // Google OAuth client ID
	GoogleClientSecret      string        `json:"googleClientSecret"`      // Google OAuth client secret
	GlobalPassword          string        `json:"globalPassword"`          // Global password for certain operations
	Branding                Branding      `json:"branding"`                // Branding configuration
	Permissions             struct {      // Permissions configuration
		AuthActions  []string `json:"authActions"`  // List of actions requiring authentication
		GuestActions []string `json:"guestActions"` // List of actions accessible to guests
	}
	Graphql struct { // GraphQL configuration
		Active              bool        `json:"active"`              // Enable or disable GraphQL
		ApiRoute            string      `json:"apiRoute"`            // GraphQL API route
		ApiAuthRoute        string      `json:"apiAuthRoute"`        // GraphQL authentication API route
		CustomTypes         string      `json:"customTypes"`         // Path to custom GraphQL types
		CustomResolvers     string      `json:"customResolvers"`     // Path to custom GraphQL resolvers
		CustomAuthTypes     string      `json:"customAuthTypes"`     // Path to custom authenticated GraphQL types
		CustomAuthResolvers string      `json:"customAuthResolvers"` // Path to custom authenticated GraphQL resolvers
		EnabledForClasses   interface{} `json:"enabledForClasses"`   // GraphQL enabled for specific classes
		DisabledForClasses  interface{} `json:"disabledForClasses"`  // GraphQL disabled for specific classes
		AuthResolvers       interface{} `json:"authResolvers"`       // Authenticated GraphQL resolvers
		AuthClasses         interface{} `json:"authClasses"`         // Authenticated GraphQL classes
		GuestResolvers      interface{} `json:"guestResolvers"`      // Guest GraphQL resolvers
		GuestClasses        interface{} `json:"guestClasses"`        // Guest GraphQL classes
		AuthQuery           struct {    // Authenticated GraphQL queries
			User    interface{} `json:"user"`    // User-related queries
			Account interface{} `json:"account"` // Account-related queries
		}
	}
	AuditTrail struct { // Audit trail configuration
		Enabled       bool     `json:"enabled"`       // Enable or disable audit trail recording
		ExcludeModels []string `json:"excludeModels"` // Model names to exclude from audit trail recording
	}
	Cache struct { // In-memory caches for per-request lookups; writes through the framework clear them immediately, so the TTL only bounds staleness from changes made elsewhere (another instance, or the database directly)
		TenantSeconds int `json:"tenantSeconds"` // How long a host's Tenant/TenantConfig/TenantCatch lookup is reused; 0 = default (30s), negative = disabled
		UserSeconds   int `json:"userSeconds"`   // How long a user's record is reused on an authorization server; 0 = default (10s), negative = disabled. Also how long a role/permission change made elsewhere takes to apply
	} `json:"cache"`
	Security struct { // Protection against volumetric/resource-exhaustion attacks
		ReadTimeoutSeconds       int      `json:"readTimeoutSeconds"`       // Max seconds to read the full request (headers+body); 0 = no limit (default, since uploads can be large)
		ReadHeaderTimeoutSeconds int      `json:"readHeaderTimeoutSeconds"` // Max seconds to read request headers; 0 = default (5s) — safe to leave on, headers are always small
		WriteTimeoutSeconds      int      `json:"writeTimeoutSeconds"`      // Max seconds to write the response; 0 = no limit (default, since exports/PDFs can be large)
		IdleTimeoutSeconds       int      `json:"idleTimeoutSeconds"`       // Max seconds an idle keep-alive connection stays open; 0 = default (60s)
		MaxHeaderBytes           int      `json:"maxHeaderBytes"`           // Max size of request headers in bytes; 0 = default (1MB, Go's stdlib default)
		MaxBodyBytes             int64    `json:"maxBodyBytes"`             // Max request body size in bytes; 0 = default (310MB)
		TrustProxyHeaders        bool     `json:"trustProxyHeaders"`        // Trust X-Forwarded-For/X-Real-Ip to identify clients for rate limiting and the error guard (only enable behind a trusted reverse proxy)
		RateLimit                struct { // Per-client request throttling
			Enabled           bool `json:"enabled"`           // Enable or disable rate limiting
			RequestsPerMinute int  `json:"requestsPerMinute"` // Sustained requests allowed per client per minute; 0 = default (300)
			Burst             int  `json:"burst"`             // Extra burst capacity above the sustained rate; 0 = defaults to requestsPerMinute
		} `json:"rateLimit"`
		ErrorGuard struct { // Blocks clients that flood the server with error responses (400/401/403/404/etc — scanning, brute-force, enumeration)
			Enabled           bool  `json:"enabled"`           // Enable or disable the error guard
			StatusCodes       []int `json:"statusCodes"`       // Status codes counted as abuse; empty = default ([400, 401, 403, 404])
			RequestsPerSecond int   `json:"requestsPerSecond"` // Max tracked error responses allowed per client per second before blocking; 0 = default (20)
			BlockHours        int   `json:"blockHours"`        // How long to block a source once tripped; 0 = permanent (default, a trip is treated as an attack); set to fall back to a timed block instead
		} `json:"errorGuard"`
	}
	Database struct { // Database configuration
		Kind             DatabaseType `json:"kind"`             // Type of database (e.g., mongodb, sql, mysql, local)
		Srv              bool         `json:"srv"`              // Enable SRV record lookup for MongoDB
		Host             string       `json:"host"`             // Database host address
		Port             string       `json:"port"`             // Database port
		DatabaseName     string       `json:"databaseName"`     // Name of the database
		Username         interface{}  `json:"username"`         // Database username
		Password         interface{}  `json:"password"`         // Database password
		Prefix           string       `json:"prefix"`           // Table/collection name prefix
		GenerateID       bool         `json:"generateID"`       // Automatically generate IDs for new records
		GenerateIDLength int          `json:"generateIDLength"` // Length of generated IDs

		// MongoDB connection pool and timeouts; 0 = the driver's default.
		MaxPoolSize                   int  `json:"maxPoolSize"`                   // Max connections per server; 0 = driver default (100)
		MinPoolSize                   int  `json:"minPoolSize"`                   // Connections kept open while idle; 0 = driver default (0)
		MaxConnIdleTimeSeconds        int  `json:"maxConnIdleTimeSeconds"`        // Close connections idle this long; 0 = never
		ConnectTimeoutSeconds         int  `json:"connectTimeoutSeconds"`         // Max seconds to open a connection; 0 = driver default (30s)
		ServerSelectionTimeoutSeconds int  `json:"serverSelectionTimeoutSeconds"` // Max seconds to find a usable server; 0 = driver default (30s)
		QueryTimeoutSeconds           int  `json:"queryTimeoutSeconds"`           // Max seconds for any one database operation; 0 = no limit (default, since exports and reports can run long)
		DisableAutoIndexes            bool `json:"disableAutoIndexes"`            // Skip creating indexes at startup (tenantId, foreign keys, and fields marked "index"/"unique" in database.json)
		DisableAutoMigrate            bool `json:"disableAutoMigrate"`            // MySQL/SQL: skip creating missing tables and columns from database.json at startup
	}
	Authentication struct { // Authentication configuration
		SaltRound   int    `json:"saltRound"`   // Number of salt rounds for password hashing
		Algorithm   string `json:"algorithm"`   // Hashing algorithm for passwords
		SecretToken string `json:"secretToken"` // Secret key for JWT or session tokens
		CryptoJsKey string `json:"cryptoJsKey"` // Cryptographic key for client-side encryption
		CryptoJsIv  string `json:"cryptoJsIv"`  // Initialization vector for client-side encryption
	}
	Ports struct { // Port configuration
		Secure    bool `json:"secure"`    // Enable secure ports (HTTPS/SSL)
		Server    int  `json:"server"`    // HTTP server port
		SSLServer int  `json:"sslServer"` // HTTPS/SSL server port
		Redis     int  `json:"redis"`     // Redis server port
	}
	Mail struct { // Mail configuration
		Smtp SMTPConfig `json:"smtp"` // SMTP server configuration
	}
	ApiGateway struct { // API Gateway configuration for external services
		SMS      SMSGatewayConfig      `json:"sms"`      // SMS gateway configuration
		Whatsapp WhatsappGatewayConfig `json:"whatsapp"` // WhatsApp gateway configuration
		Payment  PaymentGatewayConfig  `json:"payment"`  // Payment gateway configuration
	}
	AdminCredential struct { // Admin user credentials
		Username interface{} `json:"username"` // Admin username
		Password interface{} `json:"password"` // Admin password
	}
}
