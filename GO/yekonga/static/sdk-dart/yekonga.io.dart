import 'dart:async';
import 'dart:convert';
import 'package:web_socket_channel/web_socket_channel.dart';
import 'package:web_socket_channel/status.dart' as status;

/// Flutter/Dart WebSocket client with Socket.IO-like API
/// Features:
/// - Automatic reconnection with exponential backoff
/// - Message queuing when disconnected
/// - Rooms support
/// - Acknowledgments (acks)
/// - Fluent API: socket.to('room').emit(...)
/// - Events: connect, disconnect, reconnecting, error, etc.

typedef EventCallback = void Function(dynamic data);
typedef AckCallback = void Function(dynamic response);

class YekongaSocket {
  WebSocketChannel? _channel;
  final String url;
  final Map<String, dynamic> _opts;

  final Map<String, List<EventCallback>> _eventListeners = {};
  final Set<String> _rooms = {};
  final List<_PendingMessage> _pendingMessages = [];
  final Map<String, _PendingAck> _pendingAcks = {};

  int _reconnectAttempts = 0;
  Timer? _reconnectTimer;
  bool _connecting = false;

  int _nextMsgId = 0;

  String? id;
  bool connected = false;
  bool disconnected = true;

  YekongaSocket(
    this.url, {
    bool reconnection = true,
    int reconnectionAttempts = double.infinity.toInt(),
    int reconnectionDelay = 1000,
    int reconnectionDelayMax = 5000,
    int timeout = 10000,
  }) : _opts = {
         'reconnection': reconnection,
         'reconnectionAttempts': reconnectionAttempts,
         'reconnectionDelay': reconnectionDelay,
         'reconnectionDelayMax': reconnectionDelayMax,
         'timeout': timeout,
       } {
    connect();
  }

  void connect() {
    if (_connecting) return;
    _connecting = true;

    try {
      final u = Uri.parse(url);
      final socketUrl = Uri.parse(
        '${u.origin}/yekonga.io/?ns=${u.path}',
      ).toString();

      _channel = WebSocketChannel.connect(Uri.parse(socketUrl));

      _channel!.stream.listen(
        (data) {
          try {
            final msg = jsonDecode(data as String);

            // Handle acknowledgment
            if (msg['type'] == 'ack' && msg['msgId'] != null) {
              final pending = _pendingAcks.remove(msg['msgId']);
              if (pending != null) {
                pending.timer.cancel();
                pending.ack(msg['payload'] ?? null);
              }
              return;
            }

            // Client ID from server
            if (msg['event'] == 'id') {
              id = msg['data'];
              _emitLocal('id', id);
              return;
            }

            // Regular event
            if (msg['event'] != null) {
              _emitLocal(msg['event'], msg['data']);
            }
          } catch (err) {
            print('Failed to parse message: $err');
          }
        },
        onDone: () {
          connected = false;
          disconnected = true;
          _connecting = false;
          id = null;

          _emitLocal('disconnect', {
            'code': _channel?.closeCode,
            'reason': _channel?.closeReason,
          });

          if (_opts['reconnection'] == true &&
              _reconnectAttempts < _opts['reconnectionAttempts']) {
            _scheduleReconnect();
          } else if (_reconnectAttempts >= _opts['reconnectionAttempts']) {
            _emitLocal('reconnect_failed', null);
          }
        },
        onError: (error) {
          _emitLocal('error', error);
        },
      );

      // onDone is triggered after successful connection
      connected = true;
      disconnected = false;
      _connecting = false;
      _reconnectAttempts = 0;
      _reconnectTimer?.cancel();
      _reconnectTimer = null;

      _flushPendingMessages();
      _rejoinRooms();
      _emitLocal('connect', null);
    } catch (err) {
      print('WebSocket connection failed: $err');
      _scheduleReconnect();
    }
  }

  void _scheduleReconnect() {
    if (_reconnectTimer != null) return;

    final delay = (_opts['reconnectionDelay'] * (1 << _reconnectAttempts))
        .clamp(0, _opts['reconnectionDelayMax']);

    _reconnectAttempts++;
    _emitLocal('reconnecting', {'attempt': _reconnectAttempts, 'delay': delay});

    _reconnectTimer = Timer(Duration(milliseconds: delay), () {
      _reconnectTimer = null;
      connect();
    });
  }

  void _rejoinRooms() {
    if (_rooms.isNotEmpty) {
      final rooms = _rooms.toList();
      _send({
        'type': 'rejoinRooms',
        'data': {'rooms': rooms},
      });
    }
  }

  void _flushPendingMessages() {
    while (_pendingMessages.isNotEmpty && connected) {
      final pending = _pendingMessages.removeAt(0);
      _send(pending.message, pending.ack);
    }
  }

  // === Public API ===

  YekongaSocket on(String event, EventCallback callback) {
    _eventListeners.putIfAbsent(event, () => []);
    _eventListeners[event]!.add(callback);
    return this;
  }

  YekongaSocket off(String event, [EventCallback? callback]) {
    if (callback == null) {
      _eventListeners.remove(event);
      return this;
    }

    final listeners = _eventListeners[event];
    if (listeners != null) {
      listeners.remove(callback);
    }
    return this;
  }

  YekongaSocket once(String event, EventCallback callback) {
    void wrapper(data) {
      callback(data);
      off(event, wrapper);
    }

    return on(event, wrapper);
  }

  YekongaSocket emit(String event, [dynamic data, AckCallback? ack]) {
    final msgId = ack != null ? _generateMsgId() : null;

    final message = {
      'type': 'emit',
      if (msgId != null) 'msgId': msgId,
      'data': {'event': event, 'payload': data},
    };

    _send(message, ack);
    return this;
  }

  /// Fluent room emitter: socket.to('room').emit(...)
  RoomEmitter to(String room) {
    return RoomEmitter(this, room);
  }

  /// Direct emit to room: socket.to('room', 'event', data, ack)
  YekongaSocket toRoom(
    String room,
    String event, [
    dynamic data,
    AckCallback? ack,
  ]) {
    final msgId = ack != null ? _generateMsgId() : null;

    final message = {
      'type': 'toRoom',
      if (msgId != null) 'msgId': msgId,
      'data': {'room': room, 'event': event, 'payload': data},
    };

    _send(message, ack);
    return this;
  }

  YekongaSocket join(String room) {
    if (!_rooms.contains(room)) {
      _rooms.add(room);
      _send({
        'type': 'join',
        'data': {'room': room},
      });
    }
    return this;
  }

  YekongaSocket leave(String room) {
    if (_rooms.remove(room)) {
      _send({
        'type': 'leave',
        'data': {'room': room},
      });
    }
    return this;
  }

  YekongaSocket createChannel(String channel) {
    _send({
      'type': 'createChannel',
      'data': {'channel': channel},
    });
    return join(channel);
  }

  void disconnect() {
    _opts['reconnection'] = false;
    _reconnectTimer?.cancel();
    _reconnectTimer = null;
    _channel?.sink.close(status.goingAway);
  }

  // === Private helpers ===

  void _emitLocal(String event, [dynamic data]) {
    final listeners = _eventListeners[event] ?? [];
    for (final cb in listeners) {
      cb(data);
    }
  }

  String _generateMsgId() {
    return 'c_${DateTime.now().millisecondsSinceEpoch}_${_nextMsgId++}';
  }

  void _send(Map<String, dynamic> message, [AckCallback? ack]) {
    final isOpen =
        _channel != null &&
        _channel!.closeCode == null; // sink is open if no close code

    if (!connected || !isOpen) {
      if (ack != null || message['msgId'] != null) {
        _pendingMessages.add(_PendingMessage(message, ack));
      }
      return;
    }

    if (message['msgId'] != null && ack != null) {
      final timer = Timer(Duration(milliseconds: _opts['timeout']), () {
        if (_pendingAcks.containsKey(message['msgId'])) {
          ack(Error('Acknowledgment timeout'));
          _pendingAcks.remove(message['msgId']);
        }
      });

      _pendingAcks[message['msgId']] = _PendingAck(ack, timer);
    }

    try {
      _channel!.sink.add(jsonEncode(message));
    } catch (err) {
      print('Failed to send message: $err');
      if (ack != null || message['msgId'] != null) {
        _pendingMessages.add(_PendingMessage(message, ack));
      }
    }
  }
}

/// Fluent room emitter
class RoomEmitter {
  final YekongaSocket socket;
  final String room;

  RoomEmitter(this.socket, this.room);

  RoomEmitter emit(String event, [dynamic data, AckCallback? ack]) {
    socket.toRoom(room, event, data, ack);
    return this;
  }
}

/// Internal classes
class _PendingMessage {
  final Map<String, dynamic> message;
  final AckCallback? ack;

  _PendingMessage(this.message, this.ack);
}

class _PendingAck {
  final AckCallback ack;
  final Timer timer;

  _PendingAck(this.ack, this.timer);
}

// Example usage:
/*
final socket = YekongaSocket('wss://example.com/chat');

socket
  .on('connect', () => print('Connected as ${socket.id}'))
  .on('disconnect', () => print('Disconnected'))
  .on('message', (msg) => print('Server: $msg'));

socket.emit('chat_message', 'Hello!', (ack) {
  print('Ack: $ack');
});

socket.join('general');
socket.to('general').emit('chat_message', 'Hi room!');

socket.once('welcome', (data) {
  print('Welcome: $data');
});
*/
