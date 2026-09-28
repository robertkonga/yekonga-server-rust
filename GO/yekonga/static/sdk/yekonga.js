// @ts-nocheck
class YekongaCloudFunction {
    constructor(options) {
        this.socket;
        this.socketSystem;
        this.customSockets = {}
        this._options;
        this._webRTC = null;
        this._listeners = {};
        this._socketId;

        this._options = (options) ? options : {};
        if (!this._options.host) {
            this._options.host = window.YekongaServer.Host;
        }
        this._socketId = this.uuid;
        this._initSocket();
    }

    webRTC({userId, roomId, profile}) {
        const url = `${this.url}/peer`; 
        if(!this._webRTC) {
            this._webRTC = new WebRTC({userId, profile, url});
        }

        if(roomId) {
            this._webRTC.room(roomId);
        }

        return this._webRTC;
    } 

    live(model, query, callback, name, id = null) {
        this.graphql(query, callback, name, true);
        this._listeners[name] = { id, model, query, callback, loading: false };
    }

    liveUpdate(query, name) {
        if (this._listeners[name]) {
            this._listeners[name].query = query;

            this.graphql(this._listeners[name].query, this._listeners[name].callback, name, true);
        }
    }

    liveRemove(id) {
        this._listeners[id] = undefined;
    }

    async fetch(query, callback = null) {
        return await this.graphql(query, callback);
    }

    async graphql(query, callback = null, name = null, isSocket = false, data = null) {
        if (name && this._listeners[name] && this._listeners[name].loading) return;
        if (name && this._listeners[name]) this._listeners[name].loading = true;
        
        const url = `${this.url}/${window.YekongaServer.graphql}`;
        const token = window.localStorage.getItem('token');

        let content = {};
        let headers = {
            'Accept': 'application/json',
            'Content-Type': 'application/json',
            'Upgrade-Insecure-Requests': 1
        }

        if (token && token.trim() != '') headers['Authorization'] = `Bearer ${token}`;

        let bodyFormData = {
            query: query,
            variables: ((data) ? { input: data } : null),
            operationName: null,
        }

        // if (window.systemConfig && window.systemConfig.endToEndEncryption) {
        //     secureBody = { encrypted: Secret.encrypt(body) };
        // } else {
        //     secureBody = body;
        // }

        if (isSocket) {
            return this._graphqlSocket(name, bodyFormData, headers);
        } else {
            try {
                await window.axios.post(`${url}`, bodyFormData, {
                    method: 'POST',
                    headers: headers,
                }).then((response) => {
                    var body = response.data;
                    content = (body.data) ? body.data : body;
                    if (typeof callback == 'function') callback(content);
                }).catch((error) => {
                    if (error.response) {
                        content.errors = error.response.data;
                    } else if (error.request) {
                        content.errors = { message: error.request.statusText };
                        if (!content.errors.message || (content.errors.message && content.errors.message.trim() == '')) {
                            content.errors.message = error.toString();
                            window.customAlert(content.errors.message, 'danger', 8000);
                        }
                    } else {
                        content.errors = { message: error.message };
                    }
                });
            } catch (error) {
                content.errors = error;
            }
        }

        if (name && this._listeners[name]) this._listeners[name].loading = false;
        if (content.errors) console.error(content.errors);

        return content;
    }

    setChannel(namespace, options = {}) {
        if (window.YekongaSocket) {
            if(!this.customSockets[namespace]) {
                this.customSockets[namespace] = window.YekongaSocket(`${this.socketUrl}/${namespace}`, options);
            }
        }

        return null;
    }

    setSocket(namespace, options = {}) {
        this.setChannel(namespace, options);

        return this.of(namespace);
    }

    of(namespace) {
        return this.customSockets[namespace];
    }

    _initSocket() {
        const $this = this;
        let socketChecker = setInterval(() => {
            if (window.YekongaSocket) {
                $this._setSocketListeners();

                clearInterval(socketChecker);
                socketChecker = undefined;
            }
        }, 500);
    }

    _setSocketListeners() {
        const $this = this;
        const proto = (window.YekongaServer.Proto == 'https') ? 'wss' : 'ws';
        
        const token = window.localStorage.token || "MY_SECRET_TOKEN";
        const options = {
            extraHeaders: {},
            reconnection: true,
            reconnectionAttempts: 10,
            upgrade: true,  
            transports: [
                // "polling",
                "websocket",
            ],
            withCredentials: false
        }; 

        if (token && token.trim() != '') {
            options.extraHeaders.Authorization = `Bearer ${token}`;
            options.auth = { token };
            options.query = { token }
        }
        
        if (window.YekongaSocket) {
            this.socket = window.YekongaSocket(`${this.socketUrl}`, options);
            this.socketSystem = window.YekongaSocket(`${this.socketUrl}/system`, options);

            this.socket.on('connect', () => {
                console.debug(`${$this._socketId} connect`);

                $this.socket.emit('subscribe', $this._socketId);
                $this._refreshListeners($this);
            })

            this.socket.on('message', (data) => {
                console.log('message from server', data)
            })

            this.socket.on('graphql-response', ({ listener, body }) => {
                if ($this._listeners[listener] && typeof $this._listeners[listener].callback == 'function') {
                    $this._listeners[listener].callback(body);
                    $this._listeners[listener].loading = false;
                }
            })

            this.socket.on('database', ({ action, model, id }) => {
                console.log(`Database ${action} on model ${model} with id ${id}`);

                $this._refreshListeners($this, model, action, id);
            });
        }

    }

    _refreshListeners($this, model = null, action = null, id = null) {
        for (const name in $this._listeners) {
            if (Object.hasOwnProperty.call($this._listeners, name)) {
                const elem = $this._listeners[name];
                if (elem) {
                    if (!model) {
                        $this.graphql(elem.query, elem.callback, name, true);
                    } else {
                        if (elem && !elem.loading && typeof elem.callback == 'function') {
                            if (elem.id && id) {
                                if ((Array.isArray(id) && id.includes(elem.id)) || elem.id == id) {
                                    $this.graphql(elem.query, elem.callback, name, true);
                                }
                            } else {
                                if (Array.isArray(elem.model) && elem.model.includes(model)) {
                                    $this.graphql(elem.query, elem.callback, name, true);
                                } else if (elem.model == model) {
                                    $this.graphql(elem.query, elem.callback, name, true);
                                }
                            }
                        }
                    }
                }
            }
        }
    }

    _graphqlSocket(name, body, headers) {
        if (this.socket) {
            this.socket.emit('graphql-request', {
                room: this._socketId,
                listener: name,
                body: body,
                headers: headers
            });
        }
    }

    get url() {
        if(this._options.bassUrl) {
            return this._options.bassUrl;
        }

        const proto = window.YekongaServer.Proto;
        const host = this._options.host;
        
        return `${proto}://${host}`;
    }
    
    get socketUrl() {
        const proto = (window.YekongaServer.Proto == 'https') ? 'wss' : 'ws';
        const host = this._options.host;
        
        if(this._options.baseUrl) {
            console.log(this._options.baseUrl.split('://').pop())
            return `${proto}://${this._options.baseUrl.split('://').pop()}`;
        }

        return `${proto}://${host}`;
    }

    get uuid() {
        var dt = new Date().getTime();
        return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, function(c) {
            var r = (dt + Math.random() * 16) % 16 | 0;
            dt = Math.floor(dt / 16);
            return (c == 'x' ? r : (r & 0x3 | 0x8)).toString(16);
        });
    }
}

window.YekongaServer.app = function(name) {
    if (typeof name == 'undefined') {
        return window.YekongaServer.Applications['default'];
    }

    return window.YekongaServer.Applications[name];
}

window.YekongaServer.Config = function(name, options) {
    if (!options && typeof name == 'object') {
        window.YekongaServer.Applications['default'] = new YekongaCloudFunction(name);
    } else {
        window.YekongaServer.Applications[name] = new YekongaCloudFunction(options);

        if (!YekongaServer[name]) {
            Object.defineProperty(YekongaServer, name, {
                get: function() { return window.YekongaServer.Applications[name]; }
            });
        } else {
            console.error(`The app name is allready preserved`)
        }
    }

    return window.YekongaServer.Applications[name];
}