/**
 * Browser JavaScript Client (ES5 compatible - works in all modern and older browsers)
 * - Socket.IO-like API: rooms, acknowledgments, reconnection
 * - Exponential backoff reconnection
 * - Message queuing when disconnected
 * - Fluent interface: socket.to('room').emit(...)
 * - No ES6+ features (no let/const, arrow functions, classes with #private, etc.)
 */

function YekongaSocket(url, options) {
    if (!(this instanceof YekongaSocket)) {
        return new YekongaSocket(url, options);
    }

    // Private variables (using naming convention)
    this._ws = null;
    this._url = url;
    this._opts = {
        reconnection: true,
        reconnectionAttempts: Infinity,
        reconnectionDelay: 1000,
        reconnectionDelayMax: 5000,
        timeout: 10000,
    };

    // Merge options
    for (var key in options) {
        if (options.hasOwnProperty(key)) {
            this._opts[key] = options[key];
        }
    }

    this._eventListeners = {};
    this._rooms = {};
    this._pendingMessages = [];
    this._pendingAcks = {};

    this._reconnectAttempts = 0;
    this._reconnectTimer = null;
    this._connecting = false;

    this._nextMsgId = 0;

    // Public properties
    this.id = undefined;
    this.connected = false;
    this.disconnected = true;

    this.connect();
}

YekongaSocket.prototype.connect = function () {
    if (this._connecting) return;
    this._connecting = true;

    try {
        var u = new URL(this._url);
        var socketUrl = u.origin + '/yekonga.io/?ns=' + u.pathname;

        this._ws = new WebSocket(socketUrl);

        this._ws.onopen = function () {
            this.connected = true;
            this.disconnected = false;
            this._connecting = false;
            this._reconnectAttempts = 0;

            if (this._reconnectTimer) {
                clearTimeout(this._reconnectTimer);
                this._reconnectTimer = null;
            }

            this._flushPendingMessages();
            this._rejoinRooms();
            this._emitLocal('connect');
        }.bind(this);

        this._ws.onclose = function (event) {
            this.connected = false;
            this.disconnected = true;
            this._connecting = false;
            this.id = undefined;

            this._emitLocal('disconnect', { code: event.code, reason: event.reason });

            if (this._opts.reconnection && this._reconnectAttempts < this._opts.reconnectionAttempts) {
                this._scheduleReconnect();
            } else if (this._reconnectAttempts >= this._opts.reconnectionAttempts) {
                this._emitLocal('reconnect_failed');
            }
        }.bind(this);

        this._ws.onerror = function (error) {
            this._emitLocal('error', error);
        }.bind(this);

        this._ws.onmessage = function (event) {
            try {
                var msg = JSON.parse(event.data);

                // Handle acknowledgment
                if (msg.type === 'ack' && msg.msgId !== undefined) {
                    var pending = this._pendingAcks[msg.msgId];
                    if (pending) {
                        clearTimeout(pending.timeout);
                        pending.ack(msg.payload !== undefined ? msg.payload : null);
                        delete this._pendingAcks[msg.msgId];
                    }
                    return;
                }

                // Client ID from server
                if (msg.event === 'id') {
                    this.id = msg.data;
                    this._emitLocal('id', this.id);
                    return;
                }

                // Regular event
                if (msg.event) {
                    this._emitLocal(msg.event, msg.data);
                }
            } catch (err) {
                console.error('Failed to parse message:', err);
            }
        }.bind(this);
    } catch (err) {
        console.error('WebSocket connection failed:', err);
        this._scheduleReconnect();
    }
};

YekongaSocket.prototype._scheduleReconnect = function () {
    if (this._reconnectTimer) return;

    var delay = Math.min(
        this._opts.reconnectionDelay * Math.pow(2, this._reconnectAttempts),
        this._opts.reconnectionDelayMax
    );

    this._reconnectAttempts++;
    this._emitLocal('reconnecting', { attempt: this._reconnectAttempts, delay: delay });

    this._reconnectTimer = setTimeout(function () {
        this._reconnectTimer = null;
        this.connect();
    }.bind(this), delay);
};

YekongaSocket.prototype._rejoinRooms = function () {
    var rooms = [];
    for (var room in this._rooms) {
        if (this._rooms.hasOwnProperty(room)) {
            rooms.push(room);
        }
    }
    if (rooms.length > 0) {
        this._send({ type: 'rejoinRooms', data: { rooms: rooms } });
    }
};

YekongaSocket.prototype._flushPendingMessages = function () {
    while (this._pendingMessages.length > 0 && this.connected) {
        var item = this._pendingMessages.shift();
        this._send(item.message, item.ack);
    }
};

// === Public API ===

YekongaSocket.prototype.on = function (event, callback) {
    if (!this._eventListeners[event]) {
        this._eventListeners[event] = [];
    }
    this._eventListeners[event].push(callback);
    return this;
};

YekongaSocket.prototype.off = function (event, callback) {
    if (!callback) {
        delete this._eventListeners[event];
        return this;
    }

    var listeners = this._eventListeners[event];
    if (listeners) {
        var newListeners = [];
        for (var i = 0; i < listeners.length; i++) {
            if (listeners[i] !== callback) {
                newListeners.push(listeners[i]);
            }
        }
        this._eventListeners[event] = newListeners;
    }
    return this;
};

YekongaSocket.prototype.once = function (event, callback) {
    var self = this;
    function wrapper(data) {
        callback(data);
        self.off(event, wrapper);
    }
    return this.on(event, wrapper);
};

YekongaSocket.prototype.emit = function (event, data, ack) {
    // Handle: emit(event), emit(event, data), emit(event, data, ack)
    if (typeof data === 'function') {
        ack = data;
        data = undefined;
    }

    var msgId = ack ? this._generateMsgId() : undefined;

    var message = {
        type: 'emit',
        msgId: msgId,
        data: { event: event, payload: data }
    };

    this._send(message, ack);
    return this;
};

YekongaSocket.prototype.to = function (room) {
    var args = Array.prototype.slice.call(arguments);

    // If only room is provided → return fluent interface
    if (args.length === 1) {
        var self = this;
        return {
            emit: function (event, data, ack) {
                return self.to(room, event, data, ack);
            }
        };
    }

    // to(room, event, [data], [ack])
    var event = args[1];
    var data = args[2];
    var ack = args[3];

    if (typeof data === 'function') {
        ack = data;
        data = undefined;
    }

    var msgId = ack ? this._generateMsgId() : undefined;

    var message = {
        type: 'toRoom',
        msgId: msgId,
        data: { room: room, event: event, payload: data }
    };

    this._send(message, ack);
    return this;
};

YekongaSocket.prototype.join = function (room) {
    if (!this._rooms[room]) {
        this._rooms[room] = true;
        this._send({ type: 'join', data: { room: room } });
    }
    return this;
};

YekongaSocket.prototype.leave = function (room) {
    if (this._rooms[room]) {
        delete this._rooms[room];
        this._send({ type: 'leave', data: { room: room } });
    }
    return this;
};

YekongaSocket.prototype.createChannel = function (channel) {
    this._send({ type: 'createChannel', data: { channel: channel } });
    return this.join(channel);
};

YekongaSocket.prototype.disconnect = function () {
    this._opts.reconnection = false;
    if (this._reconnectTimer) {
        clearTimeout(this._reconnectTimer);
        this._reconnectTimer = null;
    }
    if (this._ws) {
        this._ws.close();
    }
    return this;
};

// === Private helpers ===

YekongaSocket.prototype._emitLocal = function (event, data) {
    var listeners = this._eventListeners[event] || [];
    for (var i = 0; i < listeners.length; i++) {
        listeners[i](data);
    }
};

YekongaSocket.prototype._generateMsgId = function () {
    return 'c_' + Date.now() + '_' + this._nextMsgId++;
};

YekongaSocket.prototype._send = function (message, ack) {
    var isOpen = this._ws && this._ws.readyState === WebSocket.OPEN;

    if (!this.connected || !isOpen) {
        if (ack || message.msgId) {
            this._pendingMessages.push({ message: message, ack: ack });
        }
        return;
    }

    if (message.msgId && ack) {
        var timeout = setTimeout(function () {
            if (this._pendingAcks[message.msgId]) {
                ack(new Error('Acknowledgment timeout'));
                delete this._pendingAcks[message.msgId];
            }
        }.bind(this), this._opts.timeout);

        this._pendingAcks[message.msgId] = { ack: ack, timeout: timeout };
    }

    try {
        this._ws.send(JSON.stringify(message));
    } catch (err) {
        console.warn('Failed to send message:', err);
        if (ack || message.msgId) {
            this._pendingMessages.push({ message: message, ack: ack });
        }
    }
};

// Example usage:
/*
var socket = new YekongaSocket('wss://example.com/chat');

socket
  .on('connect', function() { console.log('Connected as', socket.id); })
  .on('disconnect', function() { console.log('Disconnected'); })
  .on('message', function(msg) { console.log('Server says:', msg); });

socket.emit('chat_message', 'Hello!', function(ack) {
  console.log('Server acknowledged:', ack);
});

socket.join('general');
socket.to('general').emit('chat_message', 'Hi everyone!');

socket.once('welcome', function(data) {
  console.log('Welcome:', data);
});
*/

if (typeof module !== 'undefined' && module.exports) {
    module.exports = YekongaSocket;
} else if (typeof window !== 'undefined') {
    window.YekongaSocket = YekongaSocket;
}