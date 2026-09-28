
window.socket = null;
window.socketSystem = null;

if(!window.io) {
    const socketElement = document.createElement('script');
    socketElement.src = `//dilinene.on.co/socket.io/socket.io.js`;
    document.head.append(socketElement);
}
