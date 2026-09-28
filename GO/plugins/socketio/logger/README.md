# Socket.io Logging

Override internal logger with:

```go
import (
	...
    "github.com/robertkonga/yekonga-server-go/plugins/socketio/logger"
)

func main() {
    json_logger := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
        Level: slog.LevelInfo, // Set Level for each handler
    })

	log := slog.New(json_logger).With("server", "socket.io") // attach attribute to all log lines
	logger.Log = log
}
```