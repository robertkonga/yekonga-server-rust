# YekongaServer Go

```diff
   __     __  _                                  
   \ \   / / | |                                 
    \ \_/ /__| | _ ___  _ __   __ _  __ _        
     \   / _ \ |/ / _ \| '_ \ /  ' |/  ' |       
      | |  __/   < (_) | | | |  () |  () |       
      |_|\___|_|\_\___/|_| |_|\__  |\__,_|       
                              \____|   SERVER.GO 
```

A comprehensive, production-ready Go server framework that provides out-of-the-box solutions for building scalable backend applications with REST APIs, GraphQL, database abstraction, real-time capabilities, and cloud functions.

## Features

### Core Framework
- **HTTP Server** - Built on Go's standard library with custom routing and middleware support
- **REST API** - Automatic REST API endpoint generation with configurable paths
- **GraphQL** - Full GraphQL support with schema introspection, validation, and execution
- **Real-time Communication** - WebSocket and Socket.IO support for real-time data streaming
- **Cloud Functions** - Serverless-style function execution with trigger and action support
- **Middleware System** - Flexible middleware pipeline for request/response processing

### Database Support
- **Multi-Database Support** - MySQL, MongoDB, and SQL databases
- **Database Abstraction** - Unified interface for different database backends
- **Connection Pooling** - Efficient connection management
- **JSON Data Backup** - Automatic database backup and restore capabilities
- **Query Builder** - Fluent query interface for database operations

### Security
- **JWT Authentication** - Built-in JWT token generation and validation
- **App Keys** - Application-level authentication and authorization
- **CORS Support** - Cross-Origin Resource Sharing configuration
- **SSL/TLS** - Secure HTTPS connections
- **End-to-End Encryption** - Optional encryption for sensitive data

### Developer Experience
- **Configuration Management** - JSON-based configuration
- **Logging & Monitoring** - Comprehensive logging system with multiple log levels
- **Static File Serving** - Optimized static file delivery with caching
- **Error Handling** - Centralized error handling and reporting
- **Database Introspection** - Automatic GraphQL schema generation from database structure

### Advanced Features
- **Cron Jobs** - Scheduled task execution
- **Data Models** - Automatic data model generation from database schema
- **Query Charts** - Analytics and data aggregation support
- **OTP & SMS** - One-Time Password and SMS integration
- **Admin Dashboard** - Built-in admin dashboard support
- **API Documentation** - Auto-generated API playground

---

## Table of Contents

- [Installation](#installation)
- [Getting Started](#getting-started)
- [API Usage Examples](#api-usage-examples)
- [GraphQL](#graphql)
  - [GraphQL Queries](#graphql-queries)
  - [GraphQL Mutations](#graphql-mutations)
  - [GraphQL Subscriptions](#graphql-subscriptions)
- [Helper Functions Reference](#helper-functions-reference)
  - [Type Conversion](#type-conversion)
  - [Type Checking](#type-checking)
  - [String Manipulation](#string-manipulation)
  - [Date & Time](#date-In-time)
  - [Collections & Data Structures](#collections--data-structures)
  - [File Operations](#file-operations)
  - [Network Operations](#network-operations)
  - [Data Validation](#data-validation)
- [Authentication](#authentication)
- [Configuration](#configuration)
- [Project Structure](#project-structure)
- [Testing](#testing)
- [Deployment](#deployment)
- [Troubleshooting](#troubleshooting)
- [Additional Resources](#additional-resources)

---

## Installation

Add the YekongaServer Go package to your project:

```bash
go get github.com/robertkonga/yekonga-server
```

---

## Getting Started

### 1. Create Configuration Files

Create `config.json` in your project root:

```json
{
    "appName": "My YekongaServer App",
    "version": "1.0.0",
    "description": "A powerful backend server",
    "logoUrl": "https://example.com/logo.png",
    "faviconUrl": "https://example.com/favicon.ico",
    "primaryColor": "#1976d2",
    "secondaryColor": "#306da7",
    "darkBackgroundColor": "#033360",
    "appKey": "YOUR_APP_KEY_HERE",
    "masterKey": "YOUR_MASTER_KEY_HERE",
    "enableAppKey": true,
    "connectionId": "default",
    "userIdentifiers": ["email", "phone", "username"],
    "domain": "localhost:8080",
    "protocol": "https",
    "domainAlias": ["api.localhost:8080"],
    "address": "localhost",
    "restApi": "/api",
    "restAuthApi": "/api/auth",
    "tokenKey": "YOUR_TOKEN_SECRET_KEY",
    "pdfInstances": 1,
    "tokenExpireTime": 86400,
    "secureOnly": false,
    "debug": true,
    "cors": true,
    "resetOTP": false,
    "environment": "development",
    "registerUserOnOtp": true,
    "sendOtpToSmsAndWhatsapp": false,
    "endToEndEncryption": false,
    "authPlaygroundEnable": true,
    "apiPlaygroundEnable": true,
    "enableDashboard": true,
    "allowCreateFrontend": true,
    "namingConvention": "camelCase",
    "columnNamingConvention": "snake_case",
    "namingConventionOptions": ["camelCase", "snake_case", "PascalCase"],
    "public": ["/api/auth/login", "/api/auth/register"],
    "cloud": "local",
    "logFile": "./logs/app.log",
    "indexTemplate": "./templates/index.html",
    "emailTemplate": "./templates/email.html",
    "googleApiKey": "YOUR_GOOGLE_API_KEY",
    "googleApiKeyAlt": "YOUR_GOOGLE_API_KEY_ALT",
    "googleClientId": "YOUR_GOOGLE_CLIENT_ID",
    "googleClientSecret": "YOUR_GOOGLE_CLIENT_SECRET",
    "globalPassword": "YOUR_GLOBAL_PASSWORD",
    "permissions": {
        "authActions": ["read", "create", "update", "delete"],
        "guestActions": ["read"]
    },
    "graphql": {
        "apiRoute": "/graphql",
        "apiAuthRoute": "/graphql/auth",
        "customTypes": "./graphql/types",
        "customResolvers": "./graphql/resolvers",
        "customAuthTypes": "./graphql/auth-types",
        "customAuthResolvers": "./graphql/auth-resolvers",
        "enabledForClasses": [],
        "disabledForClasses": [],
        "authResolvers": {},
        "authClasses": {},
        "guestResolvers": {},
        "guestClasses": {},
        "authQuery": {
            "user": {},
            "account": {}
        }
    },
    "database": {
        "kind": "mongodb",
        "srv": false,
        "host": "localhost",
        "port": "27017",
        "databaseName": "yekonga_app",
        "username": "root",
        "password": "password",
        "prefix": "",
        "generateId": true,
        "generateIdLength": 24
    },
    "authentication": {
        "saltRound": 10,
        "algorithm": "bcrypt",
        "tokenSecret": "YOUR_TOKEN_SECRET",
        "cryptoJsKey": "YOUR_CRYPTO_KEY",
        "cryptoJsIv": "YOUR_CRYPTO_IV"
    },
    "ports": {
        "secure": false,
        "server": 8080,
        "sslServer": 8443,
        "redis": 6379
    },
    "mail": {
        "smtp": {
            "service": "Gmail",
            "host": "smtp.gmail.com",
            "port": 587,
            "secure": false,
            "from": "noreply@example.com",
            "domain": "example.com",
            "username": "your-email@gmail.com",
            "password": "your-app-password",
            "apiKey": "YOUR_MAIL_API_KEY"
        }
    },
    "adminCredential": {
        "username": "admin",
        "password": "admin@123"
    }
}
```

Create `database.json` with your database schema:

### Database Schema (database.json)

Create `database.json` with your database schema. The database schema uses `yekonga.DatabaseStructureType` which has the following structure:

```
DatabaseStructureType = map[string]map[string]map[string]interface{}
```

#### Structure Explanation

**Level 1**: Collection/Table names (e.g., `Users`, `Posts`, `Profiles`)
**Level 2**: Field names within each collection (e.g., `name`, `email`, `userId`)
**Level 3**: Field properties (type, default, required, options, etc.)

#### Field Properties

| Property | Type | Description |
|----------|------|-------------|
| `type` | string | Data type (String, ID, Date, Boolean, Integer, Float, Text, URL, Any) |
| `default` | any | Default value for the field |
| `required` | bool | Whether field is required |
| `length` | int | Maximum length for string fields |
| `options` | []string | Allowed values for enum fields |
| `foreignKey` | string | Reference to another table (e.g., `User.id`) |
| `protected` | bool | Mark field as sensitive (e.g., passwords) |
| `resource` | string | Resource reference for relationships |

#### Example Database Schema

```json
{
    "Users": {
        "userId": {
            "type": "ID",
            "default": null,
            "required": false
        },
        "firstName": {
            "type": "String",
            "default": null,
            "required": false
        },
        "email": {
            "type": "String",
            "default": null,
            "required": false
        },
        "password": {
            "type": "String",
            "default": null,
            "required": false,
            "protected": true
        },
        "role": {
            "type": "String",
            "default": null,
            "required": false,
            "options": ["admin", "user"]
        },
        "isActive": {
            "type": "Boolean",
            "default": false,
            "required": false
        },
        "createdAt": {
            "type": "Date",
            "default": "now",
            "required": false
        }
    },
    "Posts": {
        "postId": {
            "type": "ID",
            "default": null,
            "required": false
        },
        "userId": {
            "type": "ID",
            "default": null,
            "required": false,
            "foreignKey": "User.id"
        },
        "title": {
            "type": "String",
            "default": null,
            "required": false
        },
        "content": {
            "type": "Text",
            "default": null,
            "required": false
        },
        "status": {
            "type": "String",
            "default": "draft",
            "required": false,
            "options": ["draft", "published", "archived"]
        },
        "createdAt": {
            "type": "Date",
            "default": "now",
            "required": false
        },
        "updatedAt": {
            "type": "Date",
            "default": "now",
            "required": false
        }
    }
}
```

#### Supported Data Types

| Type | Description | Example |
|------|-------------|---------|
| `ID` | Unique identifier | Primary key fields |
| `String` | Text with length limit | Names, emails |
| `Text` | Large text without length limit | Content, descriptions |
| `Integer` | Whole numbers | Age, count |
| `Float` | Decimal numbers | Price, rating |
| `Boolean` | True/false values | Active, verified |
| `Date` | Date and time | `createdAt`, `updatedAt` |
| `URL` | URL addresses | Profile images |
| `Any` | Any data type | Flexible fields |

### 2. Initialize the Server

Create `main.go`:

```go
package main

import (
    "github.com/robertkonga/yekonga-server-go/yekonga"
    "log"
)

func main() {
    // Initialize the server with config and database files
    app := yekonga.ServerConfig("./config.json", "./database.json")

    // Your custom routes and handlers here
    app.Get("/", func(req *yekonga.Request, res *yekonga.Response) {
        res.File("./public/index.html")
    })

    app.Get("/api/me", func(req *yekonga.Request, res *yekonga.Response) {
        auth := req.Auth()
        if auth == nil {
            res.Error("unauthorized", 401)
            return
        }
        res.Json(auth.ToMap())
    })

    // Setup static files
    app.Static(yekonga.StaticConfig{
        Directory:   "./public",
        PathPrefix:  "/",
        IndexFile:   "index.html",
    })

    // Start the server
    app.Start(":8080")
}
```

### 3. Run Your Server

```bash
go run main.go
```

---

## API Usage Examples

### Server Initialization Functions

- **`ServerConfig(configFile, databaseFile)`** - Initializes and returns a new server instance
- **`ServerLoad(configFile, databaseFile)`** - Initializes the server and sets the global Server variable

### REST API Routes

```go
// GET request
app.Get("/users", func(req *yekonga.Request, res *yekonga.Response) {
    users := app.ModelQuery("User").Find(nil)
    res.Json(users)
})

// POST request with data
app.Post("/users", func(req *yekonga.Request, res *yekonga.Response) {
    data := req.Body()
    user, err := app.ModelQuery("User").Create(data)
    if err != nil {
        res.Error("failed to create user", 400)
        return
    }
    res.Json(user)
})

// Path parameters
app.Get("/users/:id", func(req *yekonga.Request, res *yekonga.Response) {
    id := req.Param("id")
    user := app.ModelQuery("User").FindById(id)
    if user == nil {
        res.Error("not found", 404)
        return
    }
    res.Json(user)
})

// Query parameters
app.Get("/search", func(req *yekonga.Request, res *yekonga.Response) {
    query := req.Query("q")
    results := app.ModelQuery("User").
        Where("name", "contains", query).
        Find(nil)
    res.Json(results)
})

// PUT request (update)
app.Put("/users/:id", func(req *yekonga.Request, res *yekonga.Response) {
    id := req.Param("id")
    data := req.Body()
    user, err := app.ModelQuery("User").Update(id, data)
    if err != nil {
        res.Error("failed to update user", 400)
        return
    }
    res.Json(user)
})

// DELETE request
app.Delete("/users/:id", func(req *yekonga.Request, res *yekonga.Response) {
    id := req.Param("id")
    err := app.ModelQuery("User").Delete(id)
    if err != nil {
        res.Error("failed to delete user", 400)
        return
    }
    res.Json(map[string]string{"status": "deleted"})
})
```

### Database Queries

```go
// Basic queries
users := app.ModelQuery("User").Find(nil)
user := app.ModelQuery("User").FindById("123")
count := app.ModelQuery("User").Count()

// Filter with conditions
activeUsers := app.ModelQuery("User")
  .Where("status", "=", "active")
  .Find(nil)

// Complex filtering
results := app.ModelQuery("Post")
  .Where("status", "=", "published")
  .Where("views", ">", 100)
  .OrderBy("createdAt", "desc")
  .Take(20)
  .Skip(0)
  .Find(nil)

// Aggregation
total := app.ModelQuery("Post").Count()
avgViews := app.ModelQuery("Post").Average("views")
maxViews := app.ModelQuery("Post").Max("views")
minViews := app.ModelQuery("Post").Min("views")
totalViews := app.ModelQuery("Post").Sum("views")

// Grouping and sorting
grouped := app.ModelQuery("Post")
  .GroupBy("userId")
  .OrderBy("createdAt", "desc")
  .Find(nil)

// Create
newUser, err := app.ModelQuery("User").Create(map[string]interface{}{
  "username": "john_doe",
  "email":    "john@example.com",
  "password": "hashedpassword",
})

// Update
updated, err := app.ModelQuery("User").Update("123", map[string]interface{}{
  "email": "newemail@example.com",
})

// Delete
err := app.ModelQuery("User").Delete("123")
```

---

## GraphQL

YekongaServer Go provides comprehensive GraphQL support with automatic schema generation from your database structure. GraphQL enables efficient data fetching with flexible queries, mutations, and real-time subscriptions.

### GraphQL Schema Generation

The GraphQL schema is automatically generated based on your `database.json` structure. Each collection in your database becomes a GraphQL type with queryable fields.

#### Automatic Types from Database

Based on your database schema, the following types are automatically created:

```graphql
type User {
    userId: ID!
    email: String!
    username: String!
    password: String!
    role: String
    profile: String
    createdAt: String
    updatedAt: String
}

type Post {
    postId: ID!
    userId: ID!
    title: String!
    content: String!
    status: String
    views: Int
    likes: Int
    createdAt: String
    updatedAt: String
}
```

### GraphQL Queries

#### Query Single User

Retrieve a specific user by ID with selected fields:

```graphql
query GetUser {
    user(id: "123") {
        userId
        email
        username
        role
    }
}
```

JavaScript/Go execution:

```go
// Go example with GraphQL query
query := `
    query GetUser {
        user(id: "123") {
            userId
            email
            username
            role
        }
    }
`

result := app.GraphQL(query, nil)
```

#### Query All Users with Filtering

Retrieve multiple users with filters and pagination:

```graphql
query GetAllUsers {
    users(filter: {role: "admin"}, limit: 10, offset: 0) {
        userId
        email
        username
        role
        createdAt
    }
}
```

#### Query Users with Posts (Nested Queries)

Fetch users with their associated posts in a single query:

```graphql
query GetUsersWithPosts {
    users(limit: 5) {
        userId
        username
        email
        posts {
            postId
            title
            status
            views
        }
    }
}
```

#### Query Posts with Author Details

Retrieve posts with nested author information:

```graphql
query GetPostsWithAuthors {
    posts(filter: {status: "published"}, limit: 20) {
        postId
        title
        content
        views
        likes
        createdAt
        author {
            userId
            username
            email
        }
    }
}
```

#### Query with Aggregation

Fetch aggregated post statistics:

```graphql
query PostStatistics {
    posts(filter: {status: "published"}) {
        postId
        title
        views
        likes
    }
    postStats {
        totalPosts
        totalViews
        averageViews
        topPost {
            postId
            title
            views
        }
    }
}
```

#### Query with Search and Sorting

Search posts by title and sort results:

```graphql
query SearchPosts {
    posts(
        search: "golang"
        filter: {status: "published"}
        sort: {field: "views", order: "DESC"}
        limit: 10
    ) {
        postId
        title
        content
        views
        likes
        author {
            username
        }
    }
}
```

### GraphQL Mutations

#### Create User Mutation

Create a new user with provided data:

```graphql
mutation CreateUser {
    createUser(input: {
        username: "newuser"
        email: "newuser@example.com"
        password: "securepassword"
        role: "user"
    }) {
        userId
        username
        email
        role
        createdAt
    }
}
```

Go execution:

```go
mutation := `
    mutation CreateUser {
        createUser(input: {
            username: "newuser"
            email: "newuser@example.com"
            password: "securepassword"
            role: "user"
        }) {
            userId
            username
            email
            role
            createdAt
        }
    }
`

result := app.GraphQL(mutation, nil)
```

#### Create Post Mutation

Create a new post for a user:

```graphql
mutation CreatePost {
    createPost(input: {
        userId: "123"
        title: "Getting Started with GraphQL"
        content: "GraphQL is a query language for APIs..."
        status: "published"
    }) {
        postId
        title
        status
        createdAt
        author {
            username
        }
    }
}
```

#### Update User Mutation

Update user profile information:

```graphql
mutation UpdateUser {
    updateUser(id: "123", input: {
        email: "newemail@example.com"
        role: "moderator"
    }) {
        userId
        username
        email
        role
        updatedAt
    }
}
```

#### Update Post Mutation

Update post status and content:

```graphql
mutation UpdatePost {
    updatePost(id: "post123", input: {
        title: "Updated Title"
        content: "Updated content here..."
        status: "published"
    }) {
        postId
        title
        status
        updatedAt
    }
}
```

#### Delete User Mutation

Delete a user:

```graphql
mutation DeleteUser {
    deleteUser(id: "123") {
        success
        message
    }
}
```

#### Delete Post Mutation

Delete a post:

```graphql
mutation DeletePost {
    deletePost(id: "post123") {
        success
        message
    }
}
```

#### Batch Operations

Create multiple posts in a single mutation:

```graphql
mutation CreateMultiplePosts {
    createPost1: createPost(input: {
        userId: "123"
        title: "First Post"
        content: "Content..."
        status: "published"
    }) {
        postId
        title
    }
    
    createPost2: createPost(input: {
        userId: "123"
        title: "Second Post"
        content: "Content..."
        status: "draft"
    }) {
        postId
        title
    }
}
```

### GraphQL Subscriptions

Real-time subscriptions allow clients to receive updates when data changes.

#### User Created Subscription

Subscribe to new user creations:

```graphql
subscription OnUserCreated {
    userCreated {
        userId
        username
        email
        role
        createdAt
    }
}
```

#### Post Updated Subscription

Subscribe to post updates:

```graphql
subscription OnPostUpdated {
    postUpdated {
        postId
        title
        status
        updatedAt
        author {
            username
        }
    }
}
```

#### Post Status Changed Subscription

Subscribe to specific post status changes:

```graphql
subscription OnPostStatusChanged {
    postStatusChanged(filter: {oldStatus: "draft", newStatus: "published"}) {
        postId
        title
        oldStatus
        newStatus
        updatedAt
    }
}
```

### GraphQL Configuration

Configure GraphQL in `config.json`:

| Property | Type | Description |
|----------|------|-------------|
| `enableGraphql` | Boolean | Enable/disable GraphQL endpoint |
| `graphqlPath` | String | GraphQL endpoint path (default: `/graphql`) |
| `enableIntrospection` | Boolean | Enable schema introspection |
| `enablePlayground` | Boolean | Enable GraphQL playground (default: `/graphql-playground`) |
| `maxQueryDepth` | Integer | Maximum query nesting depth |
| `maxQueryComplexity` | Integer | Maximum query complexity score |
| `timeoutMs` | Integer | Query execution timeout in milliseconds |

Example configuration:

```json
{
    "graphql": {
        "enableGraphql": true,
        "graphqlPath": "/graphql",
        "enableIntrospection": true,
        "enablePlayground": true,
        "maxQueryDepth": 10,
        "maxQueryComplexity": 1000,
        "timeoutMs": 5000
    }
}
```

---

## Middleware

```go
// Request middleware
app.Middleware(func(req *yekonga.Request, res *yekonga.Response) error {
    log.Printf("%s %s", req.Method(), req.URL())
    return nil
}, yekonga.MiddlewareTypeRequest)

// Authentication middleware
app.Use(func(req *yekonga.Request, res *yekonga.Response) error {
    // Skip auth for public routes
    if strings.HasPrefix(req.URL(), "/api/auth/") {
        return nil
    }

    auth := req.Auth()
    if auth == nil {
        res.Error("unauthorized", 401)
        return fmt.Errorf("unauthorized")
    }
    return nil
})

// Custom validation middleware
app.Use(func(req *yekonga.Request, res *yekonga.Response) error {
    contentType := req.Header("Content-Type")
    if req.Method() == "POST" && contentType == "" {
        res.Error("Content-Type header required", 400)
        return fmt.Errorf("missing content-type")
    }
    return nil
})
```

### Cloud Functions

```go
// Define a basic cloud function
app.Define("sendWelcomeEmail", func(data interface{}, rc *yekonga.RequestContext) (interface{}, error) {
    // Email sending logic
    return map[string]string{"status": "sent"}, nil
})

// Database trigger - after finding records
app.AfterFind("User", nil, nil, func(rc *yekonga.RequestContext, qc *yekonga.QueryContext) (interface{}, error) {
    // Process user records after fetch
    users := qc.Data
    // Add computed fields, mask sensitive data, etc.
    return users, nil
})

// Database trigger - before creating
app.BeforeCreate("User", func(rc *yekonga.RequestContext, qc *yekonga.QueryContext) (interface{}, error) {
    data := qc.Data
    // Validate data before insert
    // Hash passwords, set defaults, etc.
    return data, nil
})

// Database trigger - after updating
app.AfterUpdate("User", func(rc *yekonga.RequestContext, qc *yekonga.QueryContext) (interface{}, error) {
    // Log updates, send notifications, etc.
    return qc.Data, nil
})
```

### Scheduled Tasks (Cron Jobs)

```go
import "time"

// Run every 24 hours
app.RegisterCronjob("daily-cleanup", 24*time.Hour, func(app *yekonga.YekongaData, t time.Time) {
    log.Println("Running cleanup job at", t)
    // Cleanup database, delete old records, etc.
})

// Run every hour
app.RegisterCronjob("hourly-stats", time.Hour, func(app *yekonga.YekongaData, t time.Time) {
    log.Println("Updating statistics at", t)
})

// Run every minute
app.RegisterCronjob("check-notifications", time.Minute, func(app *yekonga.YekongaData, t time.Time) {
    // Process notifications, send alerts, etc.
})

// Run at specific times
app.RegisterCronjobAt(
    "daily-report",
    yekonga.JobFrequencyDaily,
    time.Now().Add(24*time.Hour),
    func(app *yekonga.YekongaData, t time.Time) {
        log.Println("Daily report generated at", t)
    },
)
```

### Static File Serving

```go
// Serve static files from public directory
app.Static(yekonga.StaticConfig{
    Directory:   "./public",
    PathPrefix:  "/public",
    IndexFile:   "index.html",
    Extensions:  []string{".html", ".css", ".js", ".jpg", ".png", ".gif"},
    CacheMaxAge: 3600, // Cache for 1 hour
})

// Multiple static directories
app.Static(yekonga.StaticConfig{
    Directory:   "./assets",
    PathPrefix:  "/assets",
    CacheMaxAge: 86400, // Cache for 1 day
})

app.Static(yekonga.StaticConfig{
    Directory:   "./uploads",
    PathPrefix:  "/downloads",
    CacheMaxAge: 0, // No caching
})
```

### Error Handling

```go
app.Get("/products/:id", func(req *yekonga.Request, res *yekonga.Response) {
    id := req.Param("id")
    if id == "" {
        res.Error("id required", 400)
        return
    }

    product := app.ModelQuery("Product").FindById(id)
    if product == nil {
        res.Error("not found", 404)
        return
    }

    res.Status(200)
    res.Header("Content-Type", "application/json")
    res.Json(product)
})
```

---

## Helper Functions Reference

The helper package provides 120+ utility functions for common operations.

### Type Conversion

#### `ToInt(value interface{}) int`
Converts a value to an integer.
```go
result := helper.ToInt("42")           // 42
result := helper.ToInt(3.14)           // 3 (truncated)
result := helper.ToInt("invalid")      // 0 (default)
```

#### `ToFloat(value interface{}) float64`
Converts a value to a float64.
```go
result := helper.ToFloat("3.14")       // 3.14
result := helper.ToFloat(42)           // 42.0
result := helper.ToFloat("invalid")    // 0.0 (default)
```

#### `ToJson(data interface{}) string`
Converts any data structure to a JSON string with indentation.
```go
data := map[string]interface{}{"name": "John", "age": 30}
json := helper.ToJson(data)
// Returns formatted JSON string
```

#### `ToByte(data interface{}) []byte`
Converts data to a byte slice (JSON marshaled).
```go
data := map[string]interface{}{"name": "John"}
bytes := helper.ToByte(data)
// Returns: []byte(`{"name":"John"}`)
```

#### `ToMap[T any](data interface{}) map[string]T`
Generic function to convert data to a typed map.
```go
result := helper.ToMap[string](jsonData)
result := helper.ToMap[int](jsonData)
result := helper.ToMap[interface{}](jsonData)
```

#### `ToMapList[T any](data interface{}) []map[string]T`
Converts a slice of objects to a slice of typed maps.
```go
records := helper.ToMapList[interface{}](jsonArray)
// Returns: []map[string]interface{}
```

#### `ToList[T any](data interface{}) []T`
Converts an array-like structure to a typed slice.
```go
users := helper.ToList[User](jsonArray)
numbers := helper.ToList[int](jsonArray)
```

#### `ToInterface(data interface{}) (interface{}, error)`
Converts data to interface{} with error handling.
```go
result, err := helper.ToInterface(data)
if err != nil {
    log.Println("Conversion failed:", err)
}
```

#### `ConvertTo[T any](data interface{}) (T, error)`
Generic type conversion with error handling. Useful for converting maps to structs.
```go
type User struct {
    Name string `json:"name"`
    Age  int    `json:"age"`
}

user, err := helper.ConvertTo[User](data)
if err != nil {
    log.Println("Conversion failed:", err)
}
```

#### `ConvertToDataMap(data map[string]interface{}) datatype.DataMap`
Converts a map to a DataMap type.
```go
dataMap := helper.ConvertToDataMap(myMap)
```

### Type Checking

#### `IsMap(data interface{}) bool`
Checks if data is a map type (supports multiple map types).
```go
if helper.IsMap(data) {
    // Handle map
}
```

#### `IsSlice(v interface{}) bool`
Checks if data is a slice.
```go
if helper.IsSlice(data) {
    // Handle slice
}
```

#### `IsList(v interface{}) bool`
Alias for `IsSlice()`. Checks if data is a list/slice.
```go
if helper.IsList(data) {
    // Handle list
}
```

#### `IsArray(v interface{}) bool`
Alias for `IsSlice()`. Checks if data is an array.
```go
if helper.IsArray(data) {
    // Handle array
}
```

#### `IsEmpty(value interface{}) bool`
Checks if a value is empty (nil, empty string, empty slice/map).
```go
if helper.IsEmpty(value) {
    // Value is empty
}
```

#### `IsNotEmpty(value interface{}) bool`
Checks if a value is not empty.
```go
if helper.IsNotEmpty(value) {
    // Value has content
}
```

#### `IsNumeric(value interface{}) bool`
Checks if a value is numeric (int, float, or numeric string).
```go
if helper.IsNumeric("123") {
    // It's numeric
}
```

#### `IsPointer(v interface{}) bool`
Checks if a value is a pointer.
```go
if helper.IsPointer(data) {
    // It's a pointer
}
```

#### `IsSliceOfMapStringInterface(v interface{}) bool`
Checks if data is a slice of map[string]interface{}.
```go
if helper.IsSliceOfMapStringInterface(data) {
    // It's a slice of maps
}
```

### Identifier Checking

#### `IsUsernameIdentifier() bool`
Checks if "username" is a configured user identifier.
```go
if helper.IsUsernameIdentifier() {
    // Username is enabled as identifier
}
```

#### `IsPhoneIdentifier() bool`
Checks if "phone" is a configured user identifier.
```go
if helper.IsPhoneIdentifier() {
    // Phone is enabled as identifier
}
```

#### `IsEmailIdentifier() bool`
Checks if "email" is a configured user identifier.
```go
if helper.IsEmailIdentifier() {
    // Email is enabled as identifier
}
```

#### `IsWhatsappIdentifier() bool`
Checks if "whatsapp" is a configured user identifier.
```go
if helper.IsWhatsappIdentifier() {
    // WhatsApp is enabled as identifier
}
```

### String Manipulation

#### `ToCamelCase(s string) string`
Converts a string to camelCase.
```go
result := helper.ToCamelCase("hello_world")      // helloWorld
result := helper.ToCamelCase("hello-world")      // helloWorld
```

#### `ToUnderscore(text string) string`
Converts a string to snake_case.
```go
result := helper.ToUnderscore("helloWorld")      // hello_world
result := helper.ToUnderscore("HelloWorld")      // hello_world
```

#### `CamelToSnake(s string) string`
Converts camelCase or PascalCase to snake_case.
```go
result := helper.CamelToSnake("helloWorld")      // hello_world
result := helper.CamelToSnake("HelloWorld")      // hello_world
```

#### `ToVariable(s string) string`
Converts a string to a valid variable name (camelCase, lowercase first letter).
```go
result := helper.ToVariable("HelloWorld")        // helloWorld
result := helper.ToVariable("hello_world")       // helloWorld
```

#### `ToTitle(s string) string`
Converts a string to title case with spaces.
```go
result := helper.ToTitle("hello_world")          // Hello World
result := helper.ToTitle("helloWorld")           // Hello World
```

#### `ToSlug(s string) string`
Converts a string to a URL-friendly slug.
```go
result := helper.ToSlug("Hello World")           // hello-world
result := helper.ToSlug("Hello-World")           // hello-world
```

#### `ToString(s interface{}) string`
Converts any value to its string representation.
```go
result := helper.ToString(123)                   // "123"
result := helper.ToString(true)                  // "true"
```

#### `Pluralize(word string) string`
Converts a singular noun to plural form.
```go
result := helper.Pluralize("cat")                // cats
result := helper.Pluralize("box")                // boxes
result := helper.Pluralize("city")               // cities
```

#### `Singularize(word string) string`
Converts a plural noun to singular form.
```go
result := helper.Singularize("cats")             // cat
result := helper.Singularize("boxes")            // box
result := helper.Singularize("cities")           // city
```

#### `FormatPhone(phone interface{}) string`
Formats and validates a phone number.
```go
result := helper.FormatPhone("0654321098")       // 255654321098
result := helper.FormatPhone("+255654321098")    // 255654321098
```

#### `PhoneFormat(phone interface{}) string`
Alias for `FormatPhone()`.
```go
result := helper.PhoneFormat("0654321098")
```

#### `ClearSpecialCharacters(val string) string`
Removes special characters and cleans HTML tags.
```go
result := helper.ClearSpecialCharacters("<p>Hello!</p>")  // Hello
```

#### `GetRandomString(length int, typ string) string`
Generates a random string. Types: "number", "letter", "hex".
```go
result := helper.GetRandomString(10, "number")   // Random 10-digit number
result := helper.GetRandomString(8, "letter")    // Random 8-letter string
result := helper.GetRandomString(16, "hex")      // Random hex string
```

#### `GetRandomInt(length int) string`
Generates a random integer string of specified length.
```go
result := helper.GetRandomInt(6)                 // Random 6-digit number
```

#### `GetHexString(length int) string`
Generates a random hexadecimal string.
```go
result := helper.GetHexString(32)                // Random 32-char hex string
```

#### `StringLength(value string) int`
Gets the character length of a string (counts Unicode characters correctly).
```go
length := helper.StringLength("Hello")           // 5
length := helper.StringLength("你好")             // 2
```

### Date & Time

#### `StringToDatetime(value interface{}) *time.Time`
Parses multiple date/time formats.
```go
t := helper.StringToDatetime("2024-01-14")
t := helper.StringToDatetime("2024-01-14 15:30:45")
t := helper.StringToDatetime("2024-01-14T15:30:45Z")
```

#### `StringToTimeOnly(value interface{}) *time.Time`
Parses time-only strings.
```go
t := helper.StringToTimeOnly("15:30:45")
t := helper.StringToTimeOnly("3:30 PM")
```

#### `GetTimestamp(value interface{}) time.Time`
Converts a value to a UTC timestamp.
```go
ts := helper.GetTimestamp("2024-01-14")
```

#### `ToTimestampString(value interface{}, layout string) time.Time`
Parses a string with a custom time layout.
```go
ts := helper.ToTimestampString("2024", "2006")
```

#### `Yesterday() time.Time`
Returns yesterday at 00:00:00 UTC.
```go
yesterday := helper.Yesterday()
```

#### `YesterdayEnd() time.Time`
Returns yesterday at 23:59:59 UTC.
```go
yesterdayEnd := helper.YesterdayEnd()
```

#### `Today() time.Time`
Returns today at 00:00:00 UTC.
```go
today := helper.Today()
```

#### `TodayEnd() time.Time`
Returns today at 23:59:59 UTC.
```go
todayEnd := helper.TodayEnd()
```

#### `Tomorrow() time.Time`
Returns tomorrow at 00:00:00 UTC.
```go
tomorrow := helper.Tomorrow()
```

#### `TomorrowEnd() time.Time`
Returns tomorrow at 23:59:59 UTC.
```go
tomorrowEnd := helper.TomorrowEnd()
```

#### `DateStart(value interface{}) time.Time`
Returns the start of day (00:00:00) for a given date.
```go
start := helper.DateStart("2024-01-14")
```

#### `DateEnd(value interface{}) time.Time`
Returns the end of day (23:59:59) for a given date.
```go
end := helper.DateEnd("2024-01-14")
```

#### `HourStart(value interface{}) time.Time`
Returns the start of hour (XX:00:00).
```go
start := helper.HourStart("2024-01-14 15:30:45")
```

#### `HourEnd(value interface{}) time.Time`
Returns the end of hour (XX:59:59).
```go
end := helper.HourEnd("2024-01-14 15:30:45")
```

#### `WeekStart(value interface{}) time.Time`
Returns the start of the week (Monday 00:00:00).
```go
start := helper.WeekStart("2024-01-14")
```

#### `WeekEnd(value interface{}) time.Time`
Returns the end of the week (Sunday 23:59:59).
```go
end := helper.WeekEnd("2024-01-14")
```

#### `MonthStart(value interface{}) time.Time`
Returns the start of the month (1st, 00:00:00).
```go
start := helper.MonthStart("2024-01-14")
```

#### `MonthEnd(value interface{}) time.Time`
Returns the end of the month (last day, 23:59:59).
```go
end := helper.MonthEnd("2024-01-14")
```

#### `TrackTime(start *time.Time, name string)`
Tracks elapsed time and prints it.
```go
start := time.Now()
// ... do work ...
helper.TrackTime(&start, "operation")  // Prints: operation took XXms
```

### Collections & Data Structures

#### `Contains(slice []string, item string) bool`
Checks if a slice contains an item.
```go
if helper.Contains([]string{"a", "b", "c"}, "b") {
    // Item found
}
```

#### `Reverse[T interface{}](slice []T)`
Reverses a slice in place.
```go
numbers := []int{1, 2, 3}
helper.Reverse(numbers)  // [3, 2, 1]
```

#### `SortMap[T interface{}](options map[string]T) map[string]T`
Sorts a map by values.
```go
sorted := helper.SortMap(myMap)
```

#### `SortedKeys[T interface{}](options map[string]T) []string`
Returns sorted keys from a map.
```go
keys := helper.SortedKeys(myMap)
```

#### `UUID() string`
Generates a UUID v1.
```go
id := helper.UUID()  // "f47ac10b-58cc-4372-a567-0e02b2c3d479"
```

#### `ObjectID(id interface{}) bson.ObjectID`
Converts a value to a MongoDB ObjectID.
```go
objID := helper.ObjectID("507f1f77bcf86cd799439011")
objID := helper.ObjectID(123)
```

#### `ObjectIDtoString(id bson.ObjectID) string`
Converts a MongoDB ObjectID to string.
```go
str := helper.ObjectIDtoString(objID)
```

#### `GetMapValue(data interface{}, key string) interface{}`
Gets a value from a map using dot notation.
```go
value := helper.GetMapValue(data, "user.profile.name")
```

#### `GetMapString(data interface{}, key string) string`
Gets a string value from a map.
```go
name := helper.GetMapString(data, "user.name")
```

#### `GetMapInt(data interface{}, key string) int`
Gets an int value from a map.
```go
age := helper.GetMapInt(data, "user.age")
```

#### `GetMapFloat(data interface{}, key string) float64`
Gets a float value from a map.
```go
price := helper.GetMapFloat(data, "product.price")
```

#### `GetMapBoolean(data interface{}, key string) bool`
Gets a boolean value from a map.
```go
active := helper.GetMapBoolean(data, "user.active")
```

#### `GetMapDate(data interface{}, key string) time.Time`
Gets a date value from a map.
```go
createdAt := helper.GetMapDate(data, "user.createdAt")
```

#### `GetMap(data interface{}, key string) map[string]interface{}`
Gets a map value from nested data.
```go
userMap := helper.GetMap(data, "user.profile")
```

#### `GetMapArray(data interface{}, source string, keys map[string]string) []interface{}`
Extracts specific keys from array elements.
```go
users := helper.GetMapArray(data, "users", map[string]string{
    "name": "name",
    "age": "age",
})
```

#### `GetList(data interface{}, key string) []interface{}`
Gets a list of values from nested array.
```go
names := helper.GetList(data, "user.names")
```

#### `GetFirst(data interface{}) interface{}`
Gets the first element from a map or slice.
```go
first := helper.GetFirst(data)
```

#### `GetType(data interface{}) string`
Gets the type of data as a string.
```go
t := helper.GetType(data)  // "map[string]interface{}"
```

### File Operations

#### `FileExists(filename string) bool`
Checks if a file exists.
```go
if helper.FileExists("config.json") {
    // File exists
}
```

#### `CreateFile(data interface{}, filename string) error`
Creates a file with data.
```go
err := helper.CreateFile(myData, "output.json")
```

#### `SaveToFile(data interface{}, filename string) error`
Saves data to a file.
```go
err := helper.SaveToFile(myData, "data.json")
```

#### `CreateDirectory(dir string) error`
Creates a directory (and parent directories if needed).
```go
err := helper.CreateDirectory("/path/to/directory")
```

#### `CreateFolder(dir string) error`
Alias for `CreateDirectory()`.
```go
err := helper.CreateFolder("/path/to/folder")
```

#### `LoadFile(filename string) string`
Reads a file and returns its content as a string.
```go
content := helper.LoadFile("config.json")
```

#### `LoadJSONFile(filename string) (map[string]interface{}, error)`
Reads and parses a JSON file.
```go
data, err := helper.LoadJSONFile("config.json")
```

#### `DownloadFile(url, destPath string, progress func(downloaded, total int64)) error`
Downloads a file from a URL with optional progress callback.
```go
err := helper.DownloadFile(
    "https://example.com/file.zip",
    "./downloads/file.zip",
    func(downloaded, total int64) {
        fmt.Printf("Downloaded: %d/%d bytes\n", downloaded, total)
    },
)
```

#### `HomeDirectory(name string) string`
Gets the application home directory.
```go
appDir := helper.HomeDirectory("myapp")  // ~/.yekonga-server/myapp
```

#### `GetDirectoryPath() string`
Gets the path of the current executable.
```go
exePath := helper.GetDirectoryPath()
```

#### `GetPublicPath() (string, error)`
Gets the public/static files directory path.
```go
publicPath, err := helper.GetPublicPath()
```

#### `GetPath(relativePath string) string`
Converts a relative path to an absolute path.
```go
absPath := helper.GetPath("./config.json")
```

### Network Operations

#### `GetLocalIP() (string, error)`
Gets the first non-loopback local IPv4 address.
```go
ip, err := helper.GetLocalIP()  // "192.168.1.100"
```

#### `GetLocalIPS() ([]string, error)`
Gets all local IPv4 addresses.
```go
ips, err := helper.GetLocalIPS()  // ["192.168.1.100", "10.0.0.5"]
```

#### `GetPublicIP() (string, error)`
Fetches the external/public IP address.
```go
ip, err := helper.GetPublicIP()
```

#### `GetNetworkIP() (string, error)`
Gets the network address from the local IP.
```go
networkIP, err := helper.GetNetworkIP()  // "192.168.1.0"
```

#### `GetRequest(url string, headers map[string]string) (int, string, error)`
Makes an HTTP GET request.
```go
status, body, err := helper.GetRequest(
    "https://api.example.com/users",
    map[string]string{"Authorization": "Bearer token"},
)
```

#### `PostRequest(url string, headers map[string]string, body interface{}) (int, string, error)`
Makes an HTTP POST request.
```go
status, body, err := helper.PostRequest(
    "https://api.example.com/users",
    map[string]string{"Content-Type": "application/json"},
    map[string]interface{}{"name": "John"},
)
```

### Data Validation

#### `ValidateEmail(email interface{}) bool`
Validates an email address format.
```go
if helper.ValidateEmail("user@example.com") {
    // Valid email
}
```

#### `IsEmail(email interface{}) bool`
Alias for `ValidateEmail()`.
```go
if helper.IsEmail("user@example.com") {
    // Valid email
}
```

#### `IsPhone(value interface{}) bool`
Validates a phone number format.
```go
if helper.IsPhone("0654321098") {
    // Valid phone
}
```

### Data Extraction

#### `GetSortedUniqueKeys(records []datatype.DataMap) []string`
Extracts and sorts unique keys from records.
```go
keys := helper.GetSortedUniqueKeys(records)
```

#### `ConvertJSONArrayToDataArray(jsonData interface{}, headingColumns []string) ([][]string, error)`
Converts JSON array to 2D string array.
```go
data, err := helper.ConvertJSONArrayToDataArray(jsonData, []string{"name", "email"})
```

#### `ConvertJSONArrayToCSV(jsonData interface{}, headingColumns []string, filename string) (string, error)`
Converts JSON array to CSV file.
```go
path, err := helper.ConvertJSONArrayToCSV(
    jsonData,
    []string{"name", "email"},
    "export.csv",
)
```

#### `ConvertJSONArrayToExcel(jsonData interface{}, headingColumns []string, filename string) (string, error)`
Converts JSON array to Excel file.
```go
path, err := helper.ConvertJSONArrayToExcel(
    jsonData,
    []string{"name", "email", "age"},
    "export.xlsx",
)
```

#### `TextTemplate(templateString string, data map[string]interface{}, regexPattern *regexp.Regexp) string`
Replaces placeholders in a template string.
```go
result := helper.TextTemplate(
    "Hello {{name}}, you are {{age}} years old",
    map[string]interface{}{"name": "John", "age": 30},
    nil,
)
// Result: "Hello John, you are 30 years old"
```

#### `GetTextContent(template string, data map[string]interface{}) string`
Reads and processes a template file.
```go
content := helper.GetTextContent("templates/email.txt", templateData)
```

#### `GetWhatsappContent(template string, data map[string]interface{}) interface{}`
Reads a template file and parses it as JSON for WhatsApp.
```go
content := helper.GetWhatsappContent("templates/whatsapp.json", data)
```

#### `GetEmailContent(layout, template string, data map[string]interface{}) string`
Reads and processes an email template with layout.
```go
html := helper.GetEmailContent(
    "layouts/email.html",
    "templates/welcome.html",
    emailData,
)
```

#### `GetCrossPlatformIdleTime() time.Duration`
Gets the system idle time (cross-platform).
```go
idleTime := helper.GetCrossPlatformIdleTime()
```

---

## Authentication

### JWT Tokens

Tokens are configured in `config.json`:

```json
{
  "tokenKey": "YOUR_SECRET_KEY",
  "tokenExpireTime": 86400,
  "authentication": {
    "saltRound": 10,
    "algorithm": "bcrypt"
  }
}
```

Access authenticated user data:

```go
app.Get("/protected", func(req *yekonga.Request, res *yekonga.Response) {
    auth := req.Auth()
    if auth == nil {
        res.Error("unauthorized", 401)
        return
    }

    userID := auth.UserId()
    userData := auth.ToMap()
    res.Json(userData)
})
```

### App Key Authentication

Enable app key authentication in `config.json`:

```json
{
  "enableAppKey": true,
  "appKey": "YOUR_APP_KEY"
}
```

Requests must include the app key:

```bash
curl -H "X-App-Key: YOUR_APP_KEY" https://api.example.com/api/users
```

---

## Configuration

YekongaConfig parameters:

| Parameter | Type | Description |
|-----------|------|-------------|
| `appName` | string | Application name |
| `version` | string | Application version |
| `description` | string | Application description |
| `logoUrl` | string | Application logo URL |
| `faviconUrl` | string | Favicon URL |
| `primaryColor` | string | Primary theme color |
| `secondaryColor` | string | Secondary theme color |
| `darkBackgroundColor` | string | Dark mode background color |
| `appKey` | string | Application key for authentication |
| `masterKey` | string | Master key for administrative access |
| `enableAppKey` | bool | Enable app key authentication |
| `connectionID` | string | Unique connection identifier |
| `userIdentifiers` | []string | Fields used to identify users |
| `domain` | string | Domain name |
| `protocol` | string | Protocol (`http` or `https`) |
| `domainAlias` | []string | Alternative domain names |
| `address` | string | Server address/IP |
| `restApi` | string | REST API endpoint prefix |
| `restAuthApi` | string | REST Auth API endpoint prefix |
| `tokenKey` | string | JWT token key |
| `pdfInstances` | int | Number of PDF processing instances |
| `tokenExpireTime` | int | Token expiration time in seconds |
| `secureOnly` | bool | Only allow HTTPS connections |
| `debug` | bool | Enable debug logging |
| `cors` | bool | Enable CORS |
| `resetOTP` | bool | Allow OTP reset |
| `environment` | string | Environment (`development`, `staging`, `production`) |
| `registerUserOnOtp` | bool | Auto-register users on OTP verification |
| `sendOtpToSmsAndWhatsapp` | bool | Send OTP via SMS and WhatsApp |
| `endToEndEncryption` | bool | Enable end-to-end encryption |
| `authPlaygroundEnable` | bool | Enable authentication playground |
| `apiPlaygroundEnable` | bool | Enable API playground |
| `enableDashboard` | bool | Enable admin dashboard |
| `allowCreateFrontend` | bool | Allow frontend creation |
| `namingConvention` | string | Table naming convention |
| `columnNamingConvention` | string | Column naming convention |
| `namingConventionOptions` | []string | Available naming conventions |
| `public` | []string | Public routes/actions |
| `cloud` | string | Cloud provider name |
| `logFile` | string | Log file path |
| `indexTemplate` | string | Index template path |
| `emailTemplate` | string | Email template path |
| `googleApiKey` | string | Google API key |
| `googleApiKeyAlt` | string | Alternative Google API key |
| `googleClientId` | string | Google OAuth client ID |
| `googleClientSecret` | string | Google OAuth client secret |
| `globalPassword` | string | Global default password |
| `permissions` | [object](#permissions) | Permission rules and actions |
| `graphql` | [object](#graphql-configuration) | GraphQL settings |
| `database` | [object](#database-configuration) | Database connection settings |
| `authentication` | [object](#authentication-configuration) | Authentication settings |
| `ports` | [object](#ports-configuration) | Server port settings |
| `mail` | [object](#mail-configuration) | Email configuration |
| `adminCredential` | [object](#admin-credential) | Admin user credentials |

### Nested Configuration Objects

#### Permissions

| Property | Type | Description |
|----------|------|-------------|
| `authActions` | []string | Actions available to authenticated users |
| `guestActions` | []string | Actions available to guests |

#### Permissions

| Property | Type | Description |
|----------|------|-------------|
| `authActions` | []string | Actions available to authenticated users |
| `guestActions` | []string | Actions available to guests |

#### GraphQL Configuration

| Property | Type | Description |
|----------|------|-------------|
| `apiRoute` | string | GraphQL API route |
| `apiAuthRoute` | string | GraphQL auth API route |
| `customTypes` | string | Custom GraphQL types |
| `customResolvers` | string | Custom resolvers |
| `customAuthTypes` | string | Custom auth types |
| `customAuthResolvers` | string | Custom auth resolvers |
| `enabledForClasses` | interface{} | Classes with GraphQL enabled |
| `disabledForClasses` | interface{} | Classes with GraphQL disabled |
| `authResolvers` | interface{} | Auth-specific resolvers |
| `authClasses` | interface{} | Classes requiring auth |
| `guestResolvers` | interface{} | Guest-accessible resolvers |
| `guestClasses` | interface{} | Classes for guests |
| `authQuery.user` | interface{} | User query |
| `authQuery.account` | interface{} | Account query |

#### Database Configuration

| Property | Type | Description |
|----------|------|-------------|
| `kind` | string | Database type (`mongodb`, `mysql`, `sql`, or `local`) |
| `srv` | bool | Use MongoDB SRV connection |
| `host` | string | Database host |
| `port` | string | Database port |
| `databaseName` | string | Database name |
| `username` | interface{} | Database username |
| `password` | interface{} | Database password |
| `prefix` | string | Table/collection prefix |
| `generateID` | bool | Auto-generate IDs |
| `generateIDLength` | int | Generated ID length |
| `maxPoolSize` | int | Max open connections (0 = driver default) |
| `minPoolSize` | int | Connections kept open while idle (0 = driver default) |
| `maxConnIdleTimeSeconds` | int | Close connections idle this long (0 = never) |
| `connectTimeoutSeconds` | int | Max seconds to open a connection (0 = driver default) |
| `serverSelectionTimeoutSeconds` | int | MongoDB: max seconds to find a usable server (0 = driver default) |
| `queryTimeoutSeconds` | int | Max seconds for any one database operation (0 = no limit) |
| `disableAutoIndexes` | bool | Don't create indexes at startup (`tenantId`, foreign keys, and fields marked `"index"`/`"unique"` in `database.json`) |
| `disableAutoMigrate` | bool | `mysql`/`sql`: don't create missing tables and columns at startup |

`mysql` and `sql` both connect to a MySQL-compatible server (MySQL 5.7+, MariaDB 10.2+) and behave like `mongodb`: the same filters, relations, GraphQL and results. Tables and columns are created from `database.json` at startup; existing columns are never changed or dropped. Records keep the MongoDB shape (`_id`/`id` as ObjectIDs, dates as `bson.DateTime`); `Object`, `Array` and `Any` fields are stored as JSON. The MongoDB-only `$type` filter isn't supported.

#### Authentication Configuration

| Property | Type | Description |
|----------|------|-------------|
| `saltRound` | int | Bcrypt salt rounds |
| `algorithm` | string | Encryption algorithm |
| `tokenSecret` | string | Token signing secret |
| `cryptoJsKey` | string | CryptoJS encryption key |
| `cryptoJsIv` | string | CryptoJS initialization vector |

#### Ports Configuration

| Property | Type | Description |
|----------|------|-------------|
| `secure` | bool | Use secure ports |
| `server` | int | HTTP server port |
| `sslServer` | int | HTTPS server port |
| `redis` | int | Redis port |

#### Mail Configuration

| Property | Type | Description |
|----------|------|-------------|
| `smtp.service` | string | Mail service name |
| `smtp.host` | string | SMTP host |
| `smtp.port` | int | SMTP port |
| `smtp.secure` | bool | Use TLS/SSL |
| `smtp.from` | string | From address |
| `smtp.domain` | string | Domain |
| `smtp.username` | interface{} | SMTP username |
| `smtp.password` | interface{} | SMTP password |
| `smtp.apiKey` | string | Mail service API key |

#### Admin Credential

| Property | Type | Description |
|----------|------|-------------|
| `username` | interface{} | Admin username |
| `password` | interface{} | Admin password |

### Example Configuration

```json
{
  "appName": "YekongaApp",
  "version": "1.0.0",
  "environment": "production",
  "debug": false,
  "protocol": "https",
  "address": "0.0.0.0",
  "domain": "example.com",
  "restApi": "/api",
  "tokenExpireTime": 86400,
  "secureOnly": true,
  "enableAppKey": true,
  "appKey": "your-app-key-here",
  "database": {
    "kind": "mongodb",
    "host": "mongodb.example.com",
    "port": "27017",
    "databaseName": "yekonga_db",
    "username": "dbuser",
    "password": "dbpassword"
  },
  "authentication": {
    "saltRound": 10,
    "algorithm": "bcrypt",
    "tokenSecret": "your-secret-key"
  },
  "ports": {
    "secure": true,
    "server": 80,
    "sslServer": 443,
    "redis": 6379
  },
  "mail": {
    "smtp": {
      "service": "gmail",
      "host": "smtp.gmail.com",
      "port": 587,
      "secure": true,
      "from": "noreply@example.com",
      "username": "your-email@gmail.com",
      "password": "your-app-password"
    }
  }
}
```

---

## Project Structure

```
YekongaServer_Go/
├── yekonga/                # Main server package
│   ├── main.go            # Server core
│   ├── request.go         # Request handling
│   ├── response.go        # Response writing
│   ├── middleware.go      # Middleware system
│   ├── model.go           # Data models
│   ├── model_query.go     # Query builder
│   ├── graphql.go         # GraphQL support
│   ├── cloud_functions.go # Functions
│   ├── cronjob.go         # Cron jobs
│   ├── dbconnect.go       # Database abstraction
│   └── ...
├── config/                # Configuration management
├── datatype/              # Custom data types
├── helper/                # 120+ utility functions
│   ├── helpers.go
│   ├── data_extraction.go
│   ├── idle_time.go
│   └── ...
├── plugins/               # Database drivers & extensions
│   ├── database/          # Database utilities
│   ├── graphql/           # GraphQL plugin
│   ├── mongo-driver/      # MongoDB driver
│   ├── mysql/             # MySQL driver
│   └── ...
├── config.json            # Configuration file
├── database.json          # Database schema
└── README.md              # This file
```

---

## 🧪 Testing

Example test setup:

```go
package main

import (
  "testing"
  "github.com/robertkonga/yekonga-server-go/yekonga"
)

func TestUserCreation(t *testing.T) {
    app := yekonga.ServerConfig("./config.json", "./database.json")

    user, err := app.ModelQuery("User").Create(map[string]interface{}{
        "username": "testuser",
        "email":    "test@example.com",
    })
    if err != nil {
        t.Errorf("Failed to create user: %v", err)
    }

    if user == nil {
        t.Error("User is nil")
    }
}
```

---

## Deployment

### Environment-based Configuration

```go
environment := os.Getenv("APP_ENV")
if environment == "production" {
    // Use production config
    app := yekonga.ServerConfig("./config.production.json", "./database.json")
} else {
    // Use development config
    app := yekonga.ServerConfig("./config.json", "./database.json")
}
```

### Docker Deployment

Create `Dockerfile`:

```dockerfile
FROM golang:1.24

WORKDIR /app

COPY . .

RUN go mod download
RUN go build -o server .

EXPOSE 8080

CMD ["./server"]
```

Build and run:

```bash
docker build -t yekonga-app .
docker run -p 8080:8080 yekonga-app
```

---

## Troubleshooting

### Server Won't Start
- Check that configuration files are valid JSON
- Verify database connectivity
- Check that port is available
- Enable debug mode to see detailed logs

### Database Connection Issues
- Verify database credentials in configuration
- Ensure database server is running
- Check network connectivity
- Review database logs

### GraphQL Schema Not Generated
- Ensure `database.json` is properly formatted
- Verify models are correctly defined
- Check GraphQL schema validation

### Performance Issues
- Enable query caching
- Use database indexes
- Implement pagination for large datasets
- Monitor database connection pool

---

## Additional Resources

- [Config Example](config.json) - Sample configuration file
- [Database Schema Example](database.json) - Sample database schema
- [Example Code](example.go) - Code examples

---

## License

See LICENSE file in the project root.

---

## Contributing

Contributions are welcome! Please ensure:
- Code follows Go conventions
- Tests are included for new features
- Documentation is updated
- Commits are well-described

---

## Support

For issues, questions, or contributions:
- **Repository**: https://github.com/robertkonga/yekonga-server
- **Issues**: Create an issue on GitHub
- **Documentation**: Check internal development guides

---

## Version Information

- **Current Version**: 1.0.0
- **Go Version**: 1.24.0+
- **Supported Databases**: MongoDB, MySQL, SQL
- **Last Updated**: January 14, 2026

---

**YekongaServer** - Build powerful, scalable backend systems with Go, GraphQL, and REST APIs.
