# YekongaServer GraphQL Documentation

## Overview

This document provides a comprehensive guide to the GraphQL API schema for YekongaServer Go. The GraphQL schema is auto-generated based on your database structure and includes all types, queries, mutations, and subscriptions.

## Table of Contents

- [Core Types](#core-types)
- [Query Operations](#query-operations)
- [Mutation Operations](#mutation-operations)
- [Filtering & Searching](#filtering--searching)
- [Pagination](#pagination)
- [Sorting](#sorting)
- [Common Patterns](#common-patterns)

---

## Core Types

### User Type

Represents a user in the system.

```graphql
type User {
    userId: ID
    id: ID
    firstName: CustomString
    secondName: CustomString
    email: CustomString
    phone: CustomString
    username: CustomString
    password: CustomString
    profileUrl: String
    role: CustomString
    isActive: Boolean
    createdAt: Date
    updatedAt: Date
    profile: Profile
    profileUsers: [ProfileUser]
    auditTrails: [AuditTrail]
    userDevices: [UserDevice]
    reports: [Report]
}
```

### Profile Type

Represents user profile information.

```graphql
type Profile {
    profileId: ID
    id: ID
    userId: ID
    name: String
    email: String
    phone: String
    address: String
    website: String
    description: String
    profileUrl: String
    type: String
    status: String
    isPrivate: Boolean
    isApproved: Boolean
    domain: String
    subdomain: String
    defaultLanguage: String
    authProfileGroupId: ID
    deletedAt: Date
    createdAt: Date
    updatedAt: Date
    user: User
    authProfileGroup: AuthProfileGroup
    profileUsers: [ProfileUser]
    auditTrails: [AuditTrail]
    notifications: [Notification]
    reports: [Report]
}
```

### Task Type

Represents a task/todo item.

```graphql
type Task {
    taskId: ID
    id: ID
    title: String
    description: String
    status: String
    priority: String
    dueDate: Date
    notes: CustomString
    createdAt: Date
    updatedAt: Date
    taskDetails: [TaskDetail]
}
```

### ChatMessage Type

Represents a chat message in the system.

```graphql
type ChatMessage {
    chatMessageId: ID
    id: ID
    chatGroupId: ID
    chatGroupMemberId: ID
    content: String
    type: String
    media: Any
    timestamp: Date
    createdAt: Date
    updatedAt: Date
    chatGroup: ChatGroup
    chatGroupMember: ChatGroupMember
    chatMessageStatuses: [ChatMessageStatuse]
}
```

### Notification Type

Represents system notifications.

```graphql
type Notification {
    notificationId: ID
    id: ID
    userId: ID
    profileId: ID
    title: String
    content: Any
    type: String
    status: String
    link: String
    recipient: String
    recipientName: String
    senderName: String
    attachment: String
    isSeen: Boolean
    timestamp: Date
    createdAt: Date
    updatedAt: Date
    user: User
    profile: Profile
}
```

---

## Query Operations

### User Queries

#### Get Single User with All Fields

Fetch a specific user by filter criteria with comprehensive where parameters:

```graphql
query GetUser {
    user(
        where: {
            # Identity Fields
            userId: {equalTo: "123"}
            id: {equalTo: "123"}
            
            # Basic Information
            firstName: {equalTo: "John"}
            secondName: {equalTo: "Doe"}
            lastName: {matchesRegex: "^Doe"}
            
            # Contact Information
            email: {equalTo: "john@example.com"}
            phone: {equalTo: "+1234567890"}
            username: {equalTo: "johndoe"}
            whatsapp: {exists: true}
            
            # Verification Status
            isEmailVerified: {equalTo: true}
            isPhoneVerified: {equalTo: true}
            isWhatsappVerified: {equalTo: true}
            emailVerifiedAt: {greaterThan: "2025-01-01"}
            phoneVerifiedAt: {greaterThan: "2025-01-01"}
            whatsappVerifiedAt: {greaterThan: "2025-01-01"}
            
            # Account Status
            isActive: {equalTo: true}
            isBanned: {equalTo: false}
            status: {in: ["active", "pending"]}
            role: {equalTo: "user"}
            userType: {equalTo: "regular"}
            usernameType: {exists: true}
            
            # Personal Information
            gender: {equalTo: "male"}
            dateOfBirth: {lessThan: "2000-01-01"}
            
            # Profile & URL
            profileUrl: {matchesRegex: ".*profile.*"}
            
            # Timestamps
            createdAt: {greaterThan: "2025-01-01"}
            updatedAt: {lessThan: "2025-02-01"}
            lastActive: {greaterThanOrEqualTo: "2025-01-15"}
            deletedAt: {exists: false}
            
            # Authentication
            password: {exists: true}
            token: {exists: true}
            rememberToken: {exists: false}
            deviceToken: {exists: true}
            googleToken: {exists: false}
            
            # OTP & Verification Codes
            otpCode: {exists: true}
            otpCreatedAt: {greaterThan: "2025-01-15"}
            emailVerifyCode: {exists: false}
            phoneVerifyCode: {exists: false}
            whatsappVerifyCode: {exists: false}
            
            # Logical Operators
            AND: [
                {isActive: {equalTo: true}}
                {role: {in: ["admin", "moderator"]}}
            ]
            OR: [
                {status: {equalTo: "premium"}}
                {isWhatsappVerified: {equalTo: true}}
            ]
        }
        accessRole: "admin"
        route: "/users"
    ) {
        userId
        id
        firstName
        secondName
        email
        phone
        username
        role
        userType
        gender
        status
        isActive
        isBanned
        isEmailVerified
        isPhoneVerified
        isWhatsappVerified
        createdAt
        updatedAt
        lastActive
        dateOfBirth
        profile {
            profileId
            name
            email
            status
        }
    }
}
```

#### List All Users with Pagination and Advanced Filtering

Retrieve multiple users with pagination, filtering, and complex conditions:

```graphql
query ListUsers {
    userPaginate(
        limit: 10
        page: 1
        where: {
            # Basic Filters
            role: {in: ["user", "moderator"]}
            isActive: {equalTo: true}
            isBanned: {equalTo: false}
            
            # Search & Pattern Matching
            firstName: {matchesRegex: "^J"}
            email: {matchesRegex: ".*@company\\.com$"}
            username: {options: "john"}
            
            # Verification Filters
            isEmailVerified: {equalTo: true}
            AND: [
                {isPhoneVerified: {equalTo: true}}
                {createdAt: {greaterThan: "2024-01-01"}}
            ]
        }
        orderBy: {
            createdAt: DESC
        }
        distinct: [userId]
        accessRole: "admin"
    ) {
        data {
            userId
            firstName
            secondName
            email
            phone
            username
            role
            status
            isActive
            isEmailVerified
            createdAt
        }
        total
        currentPage
        lastPage
        perPage
        from
        to
    }
}
```

#### User Summary with Aggregation

Get aggregated user statistics with filtering:

```graphql
query UserStatistics {
    userSummary(
        where: {
            role: {in: ["admin", "moderator"]}
            isActive: {equalTo: true}
            createdAt: {greaterThan: "2025-01-01"}
        }
        accessRole: "admin"
    ) {
        count(
            where: {
                role: {equalTo: "admin"}
                isActive: {equalTo: true}
            }
            distinct: [userId]
        )
        max(
            targetKey: createdAt
            where: {isActive: {equalTo: true}}
        )
        min(
            targetKey: createdAt
            where: {role: {equalTo: "user"}}
        )
    }
}
```

### Profile Queries

#### Get Single Profile with All Fields

Fetch profile with comprehensive where parameters:

```graphql
query GetProfile {
    profile(
        where: {
            # Identity Fields
            profileId: {equalTo: "profile123"}
            id: {equalTo: "profile123"}
            userId: {equalTo: "user123"}
            
            # Basic Information
            name: {equalTo: "Tech Company"}
            email: {equalTo: "contact@company.com"}
            phone: {equalTo: "+1234567890"}
            
            # Web Presence
            website: {matchesRegex: "https://.*"}
            domain: {equalTo: "company.com"}
            subdomain: {equalTo: "api"}
            profileUrl: {contains: "profile"}
            
            # Profile Details
            type: {in: ["business", "personal", "enterprise"]}
            status: {equalTo: "active"}
            description: {matchesRegex: ".*technology.*"}
            address: {exists: true}
            
            # Privacy & Approval
            isPrivate: {equalTo: false}
            isApproved: {equalTo: true}
            
            # Configuration
            defaultLanguage: {equalTo: "en"}
            authProfileGroupId: {exists: true}
            
            # Timestamps
            createdAt: {greaterThan: "2025-01-01"}
            updatedAt: {lessThan: "2025-02-01"}
            deletedAt: {exists: false}
            
            # Logical Operators
            AND: [
                {status: {equalTo: "active"}}
                {isApproved: {equalTo: true}}
            ]
            OR: [
                {type: {equalTo: "business"}}
                {type: {equalTo: "enterprise"}}
            ]
            NOR: [
                {isPrivate: {equalTo: true}}
                {deletedAt: {exists: true}}
            ]
        }
        accessRole: "user"
    ) {
        profileId
        id
        userId
        name
        email
        phone
        address
        website
        description
        profileUrl
        type
        status
        isPrivate
        isApproved
        domain
        subdomain
        defaultLanguage
        createdAt
        updatedAt
        user {
            userId
            firstName
            email
        }
    }
}
```

#### List Profiles with Search and Filtering

Retrieve profiles with complex filtering and pagination:

```graphql
query SearchProfiles {
    profilePaginate(
        limit: 20
        page: 1
        where: {
            # Status & Approval
            status: {in: ["active", "approved"]}
            isApproved: {equalTo: true}
            isPrivate: {equalTo: false}
            deletedAt: {exists: false}
            
            # Search by Text
            name: {matchesRegex: "^(Tech|Digital|Software)"}
            email: {matchesRegex: ".*@company\\.com$"}
            description: {options: "technology"}
            
            # Type Filter
            type: {equalTo: "business"}
            
            # Web Presence
            website: {exists: true}
            domain: {notEqualTo: null}
            
            # Advanced Conditions
            AND: [
                {status: {equalTo: "active"}}
                {OR: [
                    {type: {equalTo: "business"}}
                    {type: {equalTo: "enterprise"}}
                ]}
            ]
        }
        orderBy: {
            createdAt: DESC
        }
        distinct: [type]
        accessRole: "public"
    ) {
        data {
            profileId
            name
            email
            type
            status
            isApproved
            website
            createdAt
        }
        total
        currentPage
        lastPage
        perPage
    }
}
```

#### Profile Summary with Statistics

Get aggregated profile data:

```graphql
query ProfileStatistics {
    profileSummary(
        where: {
            status: {equalTo: "active"}
            isApproved: {equalTo: true}
            type: {equalTo: "business"}
        }
        accessRole: "admin"
    ) {
        count(
            where: {
                type: {equalTo: "business"}
                status: {equalTo: "active"}
            }
            distinct: [profileId]
        )
        max(
            targetKey: createdAt
            where: {isApproved: {equalTo: true}}
        )
    }
}
```

### Task Queries

#### Get Task with Details

Fetch a task with all related task details:

```graphql
query GetTask {
    task(
        where: {
            taskId: {equalTo: "task123"}
        }
        accessRole: "user"
    ) {
        taskId
        title
        description
        status
        priority
        dueDate
        createdAt
        taskDetails {
            taskDetailId
            title
            description
            priority
        }
    }
}
```

#### List Tasks with Filtering

Retrieve tasks by status and priority:

```graphql
query ListTasks {
    taskPaginate(
        limit: 15
        page: 1
        where: {
            status: {equalTo: "pending"}
            priority: {in: ["high", "medium"]}
            dueDate: {greaterThan: "2025-01-15"}
        }
        orderBy: {
            priority: ASC
            dueDate: ASC
        }
        accessRole: "user"
    ) {
        data {
            taskId
            title
            status
            priority
            dueDate
        }
        total
        currentPage
        lastPage
    }
}
```

### Chat Queries

#### Get Chat Messages

Retrieve messages from a specific chat group:

```graphql
query GetChatMessages {
    chatMessagePaginate(
        limit: 50
        page: 1
        where: {
            chatGroupId: {equalTo: "group123"}
            timestamp: {greaterThan: "2025-01-01"}
        }
        orderBy: {
            timestamp: DESC
        }
        accessRole: "user"
    ) {
        data {
            chatMessageId
            content
            type
            timestamp
            chatGroupMember {
                id
                user {
                    firstName
                    email
                }
            }
        }
        total
    }
}
```

### Notification Queries

#### Get User Notifications

Fetch notifications for the current user:

```graphql
query GetNotifications {
    notificationPaginate(
        limit: 20
        page: 1
        where: {
            userId: {equalTo: "user123"}
            isSeen: {equalTo: false}
            status: {in: ["pending", "active"]}
        }
        orderBy: {
            timestamp: DESC
        }
        accessRole: "user"
    ) {
        data {
            notificationId
            title
            content
            type
            status
            isSeen
            timestamp
        }
        total
    }
}
```

---

## Mutation Operations

### User Mutations

#### Create User with All Fields

Create a new user with comprehensive field setup:

```graphql
mutation CreateUser {
    createUser(input: {
        # Basic Information
        firstName: "John"
        secondName: "M"
        lastName: "Doe"
        
        # Contact Information
        email: "john.doe@example.com"
        username: "johndoe"
        password: "securepassword123"
        phone: "+1234567890"
        whatsapp: "+1234567890"
        
        # Personal Details
        gender: "male"
        dateOfBirth: "1990-01-15"
        
        # Account Information
        role: "user"
        userType: "regular"
        usernameType: "email"
        
        # Profile URL
        profileUrl: "https://example.com/profiles/johndoe"
        
        # Status
        status: "active"
        isActive: true
        isBanned: false
    }) {
        status
        success
        message
        data {
            userId
            firstName
            email
            username
            phone
            role
            isActive
            createdAt
        }
    }
}
```

#### Update User with Selective Fields

Update specific user fields:

```graphql
mutation UpdateUser {
    updateUser(
        id: "user123"
        input: {
            # Update Name
            firstName: "Jane"
            lastName: "Smith"
            
            # Update Contact
            email: "jane.smith@example.com"
            phone: "+9876543210"
            whatsapp: "+9876543210"
            
            # Update Status
            status: "inactive"
            role: "moderator"
            isActive: false
            isBanned: false
            
            # Update Verification
            isEmailVerified: true
            isPhoneVerified: true
            isWhatsappVerified: true
        }
    ) {
        status
        success
        message
        data {
            userId
            firstName
            email
            phone
            role
            status
            isActive
            updatedAt
        }
    }
}
```

#### Delete User

Delete a user account:

```graphql
mutation DeleteUser {
    deleteUser(id: "user123") {
        status
        success
        message
    }
}
```

### Profile Mutations

#### Create Profile with All Fields

Create a new profile with comprehensive setup:

```graphql
mutation CreateProfile {
    createProfile(input: {
        # Basic Information
        userId: "user123"
        name: "Tech Company"
        email: "contact@techcompany.com"
        phone: "+1234567890"
        
        # Description & Details
        description: "A leading technology company specializing in cloud solutions"
        address: "123 Tech Street, San Francisco, CA 94105"
        
        # Web Presence
        website: "https://techcompany.com"
        domain: "techcompany.com"
        subdomain: "api"
        profileUrl: "https://example.com/profiles/techcompany"
        
        # Profile Configuration
        type: "business"
        status: "active"
        defaultLanguage: "en"
        
        # Privacy & Approval
        isPrivate: false
        isApproved: true
        authProfileGroupId: "group123"
    }) {
        status
        success
        message
        data {
            profileId
            name
            email
            type
            status
            website
            isApproved
            createdAt
        }
    }
}
```

#### Update Profile with Selective Fields

Update specific profile information:

```graphql
mutation UpdateProfile {
    updateProfile(
        id: "profile123"
        input: {
            # Update Basic Information
            name: "Updated Tech Company"
            email: "newemail@company.com"
            phone: "+9876543210"
            
            # Update Description
            description: "Updated company description with new services"
            address: "456 New Avenue, New York, NY 10001"
            
            # Update Web Presence
            website: "https://newtechcompany.com"
            domain: "newtechcompany.com"
            
            # Update Status
            status: "inactive"
            isPrivate: true
            isApproved: false
            
            # Update Language
            defaultLanguage: "es"
        }
    ) {
        status
        success
        message
        data {
            profileId
            name
            email
            status
            website
            isPrivate
            isApproved
            updatedAt
        }
    }
}
```

#### Delete Profile

Delete a profile:

```graphql
mutation DeleteProfile {
    deleteProfile(id: "profile123") {
        status
        success
        message
    }
}
```

### Task Mutations

#### Create Task

```graphql
mutation CreateTask {
    createTask(input: {
        title: "Complete Project Documentation"
        description: "Write comprehensive documentation"
        priority: "high"
        dueDate: "2025-02-15"
        status: "pending"
    }) {
        status
        success
        message
        data {
            taskId
            title
            priority
            status
            createdAt
        }
    }
}
```

#### Update Task

```graphql
mutation UpdateTask {
    updateTask(
        id: "task123"
        input: {
            status: "in-progress"
            priority: "medium"
        }
    ) {
        status
        success
        message
        data {
            taskId
            status
            priority
            updatedAt
        }
    }
}
```

### Chat Mutations

#### Send Chat Message

```graphql
mutation SendChatMessage {
    createChatMessage(input: {
        chatGroupId: "group123"
        chatGroupMemberId: "member123"
        content: "Hello everyone!"
        type: "text"
    }) {
        status
        success
        message
        data {
            chatMessageId
            content
            timestamp
            chatGroupMember {
                user {
                    firstName
                }
            }
        }
    }
}
```

#### Update Chat Message

```graphql
mutation UpdateChatMessage {
    updateChatMessage(
        id: "msg123"
        input: {
            content: "Updated message"
        }
    ) {
        status
        success
        message
        data {
            chatMessageId
            content
            updatedAt
        }
    }
}
```

### Notification Mutations

#### Create Notification

```graphql
mutation CreateNotification {
    createNotification(input: {
        userId: "user123"
        profileId: "profile123"
        title: "New Message"
        content: {message: "You have a new message"}
        type: "message"
        status: "active"
    }) {
        status
        success
        message
        data {
            notificationId
            title
            type
            status
            timestamp
        }
    }
}
```

#### Mark Notification as Seen

```graphql
mutation MarkNotificationSeen {
    updateNotification(
        id: "notif123"
        input: {
            isSeen: true
        }
    ) {
        status
        success
        message
        data {
            notificationId
            isSeen
            updatedAt
        }
    }
}
```

---

## Filtering & Searching

### Common Filter Operators

The GraphQL API supports comprehensive filtering with the following operators:

| Operator | Description | Example |
|----------|-------------|---------|
| `equalTo` | Exact match | `{email: {equalTo: "user@example.com"}}` |
| `notEqualTo` | Not equal | `{status: {notEqualTo: "deleted"}}` |
| `in` | Match any in array | `{role: {in: ["admin", "moderator"]}}` |
| `notIn` | Not in array | `{status: {notIn: ["draft", "archived"]}}` |
| `greaterThan` | Greater than | `{createdAt: {greaterThan: "2025-01-01"}}` |
| `greaterThanOrEqualTo` | Greater than or equal | `{views: {greaterThanOrEqualTo: 100}}` |
| `lessThan` | Less than | `{price: {lessThan: 1000}}` |
| `lessThanOrEqualTo` | Less than or equal | `{age: {lessThanOrEqualTo: 18}}` |
| `matchesRegex` | Regular expression | `{email: {matchesRegex: ".*@company\\.com$"}}` |
| `exists` | Field exists | `{description: {exists: true}}` |
| `all` | Contains all values | `{tags: {all: ["important", "urgent"]}}` |
| `options` | Case-insensitive match | `{name: {options: "john"}}` |

### Complex Filtering Examples

#### Filter with Multiple Conditions (AND)

```graphql
query {
    userPaginate(
        where: {
            AND: [
                {role: {equalTo: "admin"}}
                {isActive: {equalTo: true}}
                {createdAt: {greaterThan: "2025-01-01"}}
            ]
        }
    ) {
        data {
            userId
            email
            role
        }
    }
}
```

#### Filter with OR Conditions

```graphql
query {
    profilePaginate(
        where: {
            OR: [
                {type: {equalTo: "business"}}
                {type: {equalTo: "personal"}}
                {status: {equalTo: "premium"}}
            ]
        }
    ) {
        data {
            profileId
            name
            type
        }
    }
}
```

#### Combined AND/OR/NOR Conditions

```graphql
query {
    taskPaginate(
        where: {
            AND: [
                {status: {notEqualTo: "deleted"}}
                {
                    OR: [
                        {priority: {equalTo: "high"}}
                        {dueDate: {lessThan: "2025-01-20"}}
                    ]
                }
                {
                    NOR: [
                        {assignedTo: {equalTo: null}}
                        {status: {equalTo: "completed"}}
                    ]
                }
            ]
        }
    ) {
        data {
            taskId
            title
            priority
            status
        }
    }
}
```

#### Search with Regex

```graphql
query {
    userPaginate(
        where: {
            firstName: {matchesRegex: "^(John|Jane|Jack)"}
            email: {matchesRegex: ".*@company\\.com$"}
        }
    ) {
        data {
            userId
            firstName
            email
        }
    }
}
```

---

## Pagination

### Paginate Pattern

All list queries support pagination through `limit` and `page` parameters:

```graphql
query {
    userPaginate(
        limit: 20           # Items per page
        page: 1             # Page number (1-based)
        where: { ... }
        orderBy: { ... }
    ) {
        data {
            # Your fields
        }
        total              # Total number of items
        perPage            # Items per page
        currentPage        # Current page number
        lastPage           # Last page number
        from               # Start index of current page
        to                 # End index of current page
    }
}
```

### Pagination Examples

#### First Page of Results

```graphql
query {
    profilePaginate(
        limit: 25
        page: 1
        orderBy: {createdAt: DESC}
    ) {
        data { profileId name email }
        total
        currentPage
        lastPage
    }
}
```

#### Subsequent Pages

```graphql
query {
    taskPaginate(
        limit: 15
        page: 3              # Get third page
        where: {status: {equalTo: "active"}}
        orderBy: {priority: ASC}
    ) {
        data { taskId title priority }
        currentPage
        lastPage
    }
}
```

---

## Sorting

### Order By Patterns

Sort results using the `orderBy` parameter with `ASC` (ascending) or `DESC` (descending) order:

| Sort Field | Type | Example |
|-----------|------|---------|
| Field name | String | `firstName`, `createdAt`, `email` |
| Order direction | Enum | `ASC` or `DESC` |

### Sorting Examples

#### Single Field Sorting

```graphql
query {
    userPaginate(
        limit: 10
        page: 1
        orderBy: {
            createdAt: DESC    # Newest first
        }
    ) {
        data {
            userId
            firstName
            createdAt
        }
    }
}
```

#### Multiple Field Sorting

```graphql
query {
    taskPaginate(
        limit: 20
        where: {status: {equalTo: "pending"}}
        orderBy: {
            priority: ASC      # High priority first
            dueDate: ASC       # Earliest due date first
        }
    ) {
        data {
            taskId
            title
            priority
            dueDate
        }
    }
}
```

---

## Common Patterns

### Pattern: Get Count of Records

```graphql
query CountUsers {
    user(where: {role: {equalTo: "admin"}}) {
        count(
            distinct: [userId]
            where: {isActive: {equalTo: true}}
        )
    }
}
```

### Pattern: Get Max/Min Values

```graphql
query GetExtremes {
    user(where: {}) {
        max(
            targetKey: createdAt
            where: {role: {equalTo: "user"}}
        )
        min(
            targetKey: createdAt
            where: {role: {equalTo: "user"}}
        )
    }
}
```

### Pattern: Get Average Values

```graphql
query GetAverages {
    task {
        average(
            targetKey: priority
            where: {status: {equalTo: "completed"}}
        )
    }
}
```

### Pattern: Sum Aggregation

```graphql
query GetSum {
    task {
        sum(
            targetKey: hoursSpent
            where: {status: {equalTo: "completed"}}
        )
    }
}
```

### Pattern: Distinct Values

```graphql
query GetDistinct {
    userPaginate(
        limit: 100
        distinct: [role]
        where: {isActive: {equalTo: true}}
    ) {
        data {
            role
        }
    }
}
```

### Pattern: Nested Query with Filtering

```graphql
query GetUsersWithActiveProfiles {
    userPaginate(
        limit: 20
        where: {
            isActive: {equalTo: true}
            profile: {
                status: {equalTo: "active"}
            }
        }
    ) {
        data {
            userId
            firstName
            email
            profile {
                profileId
                name
                status
            }
        }
    }
}
```

### Pattern: Download Data

```graphql
query DownloadUsers {
    downloadUsers(
        download: {
            where: {role: {equalTo: "user"}}
            orderBy: {createdAt: DESC}
        }
        downloadType: CSV
        orientation: PORTRAIT
        accessRole: "admin"
    ) {
        filename
        url
        type
        size
    }
}
```

### Pattern: Data Grouping

```graphql
query GroupByRole {
    userPaginate(
        limit: 100
        groupBy: [role]
        orderBy: {createdAt: DESC}
    ) {
        data {
            role
            createdAt
        }
    }
}
```

---

## Access Control

### Role-Based Access

All queries and mutations support role-based access control through the `accessRole` parameter:

```graphql
query {
    userPaginate(
        limit: 10
        page: 1
        accessRole: "admin"    # Access level
        route: "/users"        # API route
    ) {
        data { userId email }
    }
}
```

### Available Access Roles

| Role | Description | Example |
|------|-------------|---------|
| `admin` | Full access to all resources | Administrator accounts |
| `moderator` | Elevated access with restrictions | Moderators, managers |
| `user` | Limited access to own resources | Regular users |
| `public` | Public/anonymous access | Guest users |

---

## Error Handling

Typical GraphQL error response structure:

```graphql
{
    "errors": [
        {
            "message": "User not found",
            "extensions": {
                "code": "NOT_FOUND"
            }
        }
    ],
    "data": null
}
```

---

## User Type - Complete Field Reference

### Query Fields

| Field | Type | Description | Example |
|-------|------|-------------|---------|
| `userId` | ID | Unique user identifier | `"user123"` |
| `id` | ID | Alternative user identifier | `"123"` |
| `firstName` | CustomString | User's first name | `"John"` |
| `secondName` | CustomString | User's middle name | `"M"` |
| `lastName` | CustomString | User's last name | `"Doe"` |
| `email` | CustomString | User's email address | `"john@example.com"` |
| `phone` | CustomString | User's phone number | `"+1234567890"` |
| `username` | CustomString | Unique username | `"johndoe"` |
| `password` | CustomString | Encrypted password | `"$2a$12$..."` |
| `role` | CustomString | User role/permission level | `"user"`, `"admin"`, `"moderator"` |
| `userType` | CustomString | Type of user account | `"regular"`, `"premium"`, `"enterprise"` |
| `usernameType` | CustomString | Type of username (email/phone/custom) | `"email"`, `"phone"` |
| `gender` | CustomString | User's gender | `"male"`, `"female"`, `"other"` |
| `dateOfBirth` | Date | Date of birth | `"1990-01-15"` |
| `status` | CustomString | Account status | `"active"`, `"pending"`, `"inactive"` |
| `isActive` | Boolean | Whether account is active | `true`, `false` |
| `isBanned` | Boolean | Whether user is banned | `false`, `true` |
| `isEmailVerified` | Boolean | Email verification status | `true`, `false` |
| `isPhoneVerified` | Boolean | Phone verification status | `true`, `false` |
| `isWhatsappVerified` | Boolean | WhatsApp verification status | `true`, `false` |
| `profileUrl` | String | User profile URL | `"https://example.com/profiles/johndoe"` |
| `deviceToken` | CustomString | Device push notification token | `"device_token_xyz"` |
| `googleToken` | CustomString | Google OAuth token | `"google_token_xyz"` |
| `token` | CustomString | Authentication token | `"auth_token_xyz"` |
| `rememberToken` | CustomString | Remember me token | `"remember_token_xyz"` |
| `whatsapp` | CustomString | WhatsApp contact | `"+1234567890"` |
| `emailVerifyCode` | CustomString | Email verification code | `"code123"` |
| `phoneVerifyCode` | CustomString | Phone verification code | `"code456"` |
| `whatsappVerifyCode` | CustomString | WhatsApp verification code | `"code789"` |
| `otpCode` | CustomString | One-time password | `"123456"` |
| `otpCreatedAt` | Date | OTP creation timestamp | `"2025-01-15T10:30:00Z"` |
| `emailVerifiedAt` | Date | Email verification timestamp | `"2025-01-15T10:30:00Z"` |
| `phoneVerifiedAt` | Date | Phone verification timestamp | `"2025-01-15T10:30:00Z"` |
| `whatsappVerifiedAt` | Date | WhatsApp verification timestamp | `"2025-01-15T10:30:00Z"` |
| `lastActive` | Date | Last activity timestamp | `"2025-01-15T14:30:00Z"` |
| `createdAt` | Date | Account creation timestamp | `"2024-01-01T00:00:00Z"` |
| `updatedAt` | Date | Last update timestamp | `"2025-01-15T10:30:00Z"` |
| `deletedAt` | Date | Soft delete timestamp (if deleted) | `null`, `"2025-01-15T10:30:00Z"` |

### Input Fields for Mutations

| Field | Type | Writable | Description |
|-------|------|----------|-------------|
| `firstName` | String | ✅ Yes | First name |
| `secondName` | String | ✅ Yes | Middle name |
| `lastName` | String | ✅ Yes | Last name |
| `email` | String | ✅ Yes | Email address |
| `phone` | String | ✅ Yes | Phone number |
| `username` | String | ✅ Yes | Username (typically unique) |
| `password` | String | ✅ Yes | Password (will be hashed) |
| `role` | String | ✅ Yes | User role |
| `userType` | String | ✅ Yes | User type category |
| `usernameType` | String | ✅ Yes | Username type |
| `gender` | String | ✅ Yes | Gender |
| `dateOfBirth` | Date | ✅ Yes | Date of birth |
| `status` | String | ✅ Yes | Account status |
| `isActive` | Boolean | ✅ Yes | Active status |
| `isBanned` | Boolean | ✅ Yes | Ban status |
| `profileUrl` | String | ✅ Yes | Profile URL |
| `deviceToken` | String | ✅ Yes | Device token |
| `whatsapp` | String | ✅ Yes | WhatsApp contact |
| `isEmailVerified` | Boolean | ✅ Yes | Email verified |
| `isPhoneVerified` | Boolean | ✅ Yes | Phone verified |
| `isWhatsappVerified` | Boolean | ✅ Yes | WhatsApp verified |

---

## Profile Type - Complete Field Reference

### Query Fields

| Field | Type | Description | Example |
|-------|------|-------------|---------|
| `profileId` | ID | Unique profile identifier | `"profile123"` |
| `id` | ID | Alternative profile identifier | `"123"` |
| `userId` | ID | Associated user ID | `"user123"` |
| `name` | String | Profile name/title | `"Tech Company"` |
| `email` | String | Profile email | `"contact@company.com"` |
| `phone` | String | Profile phone | `"+1234567890"` |
| `address` | String | Physical address | `"123 Tech St, San Francisco, CA"` |
| `website` | CustomString | Website URL | `"https://company.com"` |
| `description` | CustomString | Profile description/bio | `"Leading tech company..."` |
| `profileUrl` | CustomString | Profile URL | `"https://example.com/profiles/company"` |
| `type` | CustomString | Profile type | `"business"`, `"personal"`, `"enterprise"` |
| `status` | CustomString | Profile status | `"active"`, `"inactive"`, `"pending"` |
| `isPrivate` | Boolean | Privacy setting | `false`, `true` |
| `isApproved` | Boolean | Approval status | `true`, `false` |
| `domain` | CustomString | Custom domain | `"company.com"` |
| `subdomain` | CustomString | Subdomain | `"api"`, `"www"` |
| `defaultLanguage` | CustomString | Default language | `"en"`, `"es"`, `"fr"` |
| `authProfileGroupId` | ID | Auth group association | `"group123"` |
| `createdAt` | Date | Creation timestamp | `"2024-01-01T00:00:00Z"` |
| `updatedAt` | Date | Last update timestamp | `"2025-01-15T10:30:00Z"` |
| `deletedAt` | Date | Soft delete timestamp | `null`, `"2025-01-15T10:30:00Z"` |

### Input Fields for Mutations

| Field | Type | Writable | Description |
|-------|------|----------|-------------|
| `userId` | ID | ✅ Yes | Associated user ID |
| `name` | String | ✅ Yes | Profile name |
| `email` | String | ✅ Yes | Profile email |
| `phone` | String | ✅ Yes | Profile phone |
| `address` | String | ✅ Yes | Physical address |
| `website` | String | ✅ Yes | Website URL |
| `description` | String | ✅ Yes | Description/bio |
| `profileUrl` | String | ✅ Yes | Profile URL |
| `type` | String | ✅ Yes | Profile type |
| `status` | String | ✅ Yes | Profile status |
| `isPrivate` | Boolean | ✅ Yes | Privacy setting |
| `isApproved` | Boolean | ✅ Yes | Approval status |
| `domain` | String | ✅ Yes | Custom domain |
| `subdomain` | String | ✅ Yes | Subdomain |
| `defaultLanguage` | String | ✅ Yes | Default language |
| `authProfileGroupId` | ID | ✅ Yes | Auth group association |

---

## Summary

This GraphQL schema provides a powerful and flexible API for accessing YekongaServer data with:

- ✅ Comprehensive filtering and search capabilities
- ✅ Flexible pagination for large datasets
- ✅ Advanced sorting and grouping
- ✅ Aggregation functions (count, sum, avg, min, max)
- ✅ Role-based access control
- ✅ Nested object queries
- ✅ Batch operations
- ✅ Data export/download functionality

For more information, visit the GraphQL endpoint at `/graphql` with introspection enabled for interactive schema exploration.
