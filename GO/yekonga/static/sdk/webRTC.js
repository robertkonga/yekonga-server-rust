// @ts-nocheck
const variableWebRTC = 'WebRTC';

class WebRTC {

    constructor({userId, profile, url}) {
        this.url = url;
        this.localPeerId = null;
        this.localUserId = userId;
        this.localProfile = profile;
        this.peerRooms = {};
        this.candidate = null;
        this.localPeer = null;

        this.initSocket();
    }

    /** @returns {WebRTC} */
    setProfile(value) {
        this.localProfile = value;

        return this;
    }

    /** @returns {WebRTCRoom} */
    room(roomId) {
        if(roomId && !this.peerRooms[roomId]) {
            this.peerRooms[roomId] = new WebRTCRoom({
                roomId, 
                peerId: this.localPeerId,
                localProfile: this.localProfile,
                userId: this.localUserId,
                socket: this.socket,
            });
        }

        return this.peerRooms[roomId];
    }

    /** @returns {WebRTCPeer} */
    peer(roomId, peerId) {
        try {
            return this.room(roomId).peer(peerId);
        } catch (error) {
            console.log(error);
        }

        return null;
    }

    initSocket() {
        this.socket = (window.io)? window.io.connect(this.url): null;

        if(this.socket) {    
            this.socket.on('connect', this.__onConnect.bind(this));
            this.socket.on('open', this.__onOpen.bind(this));
            this.socket.on('leave', this.__removeUser.bind(this));
            this.socket.on('offer', this.__onReceiveOffer.bind(this))
            this.socket.on('answer', this.__onReceiveAnswer.bind(this))
            this.socket.on('request', this.__onOfferRequested.bind(this))
            this.socket.on('session-active', this.__onSessionActive.bind(this))
            this.socket.on('disconnect', this.__onDisconnect.bind(this));
        }
    }

    __onConnect() {
        console.log('peer connected');  

        for (const key in this.peerRooms) {
            if (Object.hasOwnProperty.call(this.peerRooms, key)) {
                this.peerRooms[key].join();
            }
        }
    }

    __onOpen({peerId, type}) {
        this.log('onOpen', {peerId});
        this.socket.on(`offer-${peerId}`, this.__onReceiveOffer.bind(this));
        this.socket.on(`answer-${peerId}`, this.__onReceiveAnswer.bind(this))

        this.localPeerId = peerId;
        for (const key in this.peerRooms) {
            if (Object.hasOwnProperty.call(this.peerRooms, key)) {
                this.peerRooms[key].localPeerId = peerId;
            }
        }
    }

    __onOfferRequested({userId, peerId, roomId, profile}) {
        this.log('onOfferRequested', {userId, peerId, roomId});

        var peerConn = this.room(roomId).peer({peerId, userId, profile, initiator: true});
    }

    __onReceiveOffer({userId, peerId, roomId, offer, profile}) {
        this.log('onOffer', {userId, peerId, roomId, offer});

        var peerConn = this.room(roomId).peer({peerId, userId, initiator: false});

        peerConn.receiveOffer({offer, userId, peerId, roomId, profile});
    }

    __onReceiveAnswer({userId, peerId, roomId, answer, profile}) {
        this.log('onAnswer', {userId, peerId, roomId, answer});

        var peerConn = this.room(roomId).peer({peerId, userId, initiator: false});

        peerConn.receiveAnswer({answer, userId, peerId, roomId, profile})
    }

    __onSessionActive({userId, peerId, roomId, offer}) {
        this.log('onSessionActive', {userId, peerId, roomId, offer});
        
        console.log('Session Active. Please come back later')
    }

    __onSendFilter({userId, peerId, roomId, offer}) {
        this.log('onSendFilter', {userId, peerId, roomId, offer});
    }

    __connectUser({userId, peerId, roomId}) {
        this.log('connectUser', {userId, peerId, roomId});

        this.room(roomId).peer({userId, peerId});
    }

    __removeUser(data) {
        var { peerId, roomId, userId } = data || {}
        this.log('removeUser', {peerId, roomId, userId});

        try {
            if(roomId && peerId) {
                this.room(roomId).removePeer(peerId)
            }
        } catch (error) {
            console.log(`WebRTC.__removeUser`, error.message);
        }
    }

    __onDisconnect(_) {
        this.log('disconnect', {data: _});
        
        try {
            for (const roomId in this.peerRooms) {
                if (Object.hasOwnProperty.call(this.peerRooms, roomId)) {
                    this.room(roomId).disconnect()
                }
            }
        } catch (error) {
            console.log(`WebRTC.__onDisconnect`, error.message);
        }
    }

    log(label, {id, userId, peerId, roomId, offer, answer}) {
        var message = [];

        if(id) message.push(`id:${id}`);
        if(peerId) message.push(`peerId:${peerId}`);
        if(userId) message.push(`userId:${userId}`);
        if(roomId) message.push(`roomId:${roomId}`);

        console.log(`${label} = ${message.join(', ')}`);
        if(offer) console.log(label, offer);
        if(answer) console.log(label, answer);
    }
}

class WebRTCRoom {
    constructor({roomId, peerId, userId, socket, localProfile}) {
        this.localProfile = localProfile;
        this.localPeerId = peerId;
        this.localUserId = userId;
        this.roomId = roomId;
        this.socket = socket;
        this.isJoined = false;
        /** @type {WebRTCPeer[]} */
        this.peers = [];
        
        this.__onChangeCallback = null;
        this.__onLeaveCallback = null;
        this.__onConnectedCallback = null;
        this.__onJoinCallback = null;
        this.__onStreamCallback = null;
        this.__onDataCallback = null;
        this.__streamMuted = false;
        this.__streamStored = null;
        this.__streamSent = null;
        this.__previewContainer = null;
        this.__streamSourceType = null;
        
        this.join();
    }

    join() {
        if(this.localPeerId && !this.isJoined) {
            this.isJoined = true;
            
            this.socket.emit('join', {
                peerId: this.localPeerId, 
                userId: this.localUserId,
                roomId: this.roomId,
                profile: this.localProfile,
            });
        }
        
    }

    setProfile(value) {
        this.localProfile = value;
    }

    onChange(callback) {
        this.__onChangeCallback = callback;
    }

    onJoin(callback) {
        this.__onJoinCallback = callback;
    }

    onConnected(callback) {
        this.__onConnectedCallback = callback;
    }

    onLeave(callback) {
        this.__onLeaveCallback = callback;
    }

    onStream(callback) {
        this.__onStreamCallback = callback;
    }

    onData(callback) {
        this.__onDataCallback = callback;
    }

    change() {
        try {
            if(typeof this.__onChangeCallback == 'function') {
                var peers = [];
                if(Array.isArray(this.peers)) {
                    peers = this.peers.map(e=>{
                        return {
                            peerId: e.peerId,
                            userId: e.userId,
                            roomId: this.roomId,
                            stream: e.remoteStream,
                            profile: e.remoteProfile,
                        }
                    })
                }
                this.__onChangeCallback(peers);
            }
        } catch (error) {
            console.error(error.message)
        }
    }

    sendData = async function(data) {
        for (const peer of this.peers) {
            peer.sendData(data);
        }

        this.change();
    }

    sendStream = async function(stream) {
        if(stream instanceof MediaStream) {
            this.__streamSourceType = 'none';
            this.__streamStored = stream;
        }
        await this.__resetStreamSource();

        for (const peer of this.peers) {
            peer.sendStream(this.__streamSent);
        }

        this.change();
    }

    start = async function(stream) {
        if(typeof stream == 'string') {
            this.__playVideoFile(stream);
        } else {
            this.__stopVideoFile();
            this.sendStream(stream);
        }
    }

    startWebcam = async function() {
        this.__streamSourceType = 'webcam';
        this.__stopVideoFile();
        await this.__resetStreamSource();

        for (const peer of this.peers) {
            peer.start(this.__streamSent);
        }
    }
    
    startScreenShare = async function() {
        this.__streamSourceType = 'screen';
        this.__stopVideoFile();
        await this.__resetStreamSource();

        for (const peer of this.peers) {
            peer.start(this.__streamSent);
        }
    }

    setSource = async function(value) {
        this.__streamSourceType = value;
        await this.__resetStreamSource();
    }

    stop() {
        try {
            for (const peer of this.peers) {
                peer.stop();
            }  
        } catch (error) {
            console.error(error.message)
        }
    }

    mute() {
        this.__streamMuted = !this.__streamMuted;
        this.start(this.__streamStored);
    }

    setPreview(view) {
        this.__previewContainer = view;
        this.__loadPreview();
    }

    leave() {
        for (const peer of this.peers) {
            peer.destroy();
        }
    }

    /** @returns {WebRTCPeer} */
    peer({userId, peerId, profile, initiator = true}) {
        var peer = this.__peer(peerId);
        if(!peer) {
            peer = new WebRTCPeer(this.socket, {
                userId, 
                peerId, 
                remotePeerId: peerId, 
                remoteProfile: profile,
                roomId: this.roomId,
                localPeerId: this.localPeerId,
                localUserId: this.localUserId,
                localProfile: this.localProfile,
                initiator,
            });

            peer.onJoin(this.__onJoinCallback);
            peer.onConnected(this.__onConnectedCallback);
            peer.onLeave(this.__onLeaveCallback);
            peer.onStream(this.__onStreamCallback);
            peer.onData(this.__onDataCallback);
            peer.sendStream(this.__streamSent);
            peer.onPeerDisconnected(this.__onPeerDisconnected.bind(this));
            peer.onPeerChanged(this.change.bind(this));
            peer.triggerJoin();
            
            this.peers.push(peer);

            this.change();
        }

        return peer;
    }

    targetPeer(peerId) {
        for (const peer of this.peers) {
            if(peer.peerId === peerId) {
                return peer;
            }
        }

        return null;
    }

    removePeer(peerId) {
        for (let i = 0; i < this.peers.length; i++) {
            if(this.peers[i].peerId === peerId) {
                this.peers[i].destroy();
                this.peers.splice(i, 1);
                break;
            }
        }

        this.change();
    }

    disconnect() {
        for (let i = 0; i < this.peers.length; i++) {
            this.peers[i].destroy();
        }

        this.peers = [];
        this.isJoined = false;
    }

    __onPeerDisconnected = async function({roomId, userId, peerId}) {
        try {
            for (let i = 0; i < this.peers.length; i++) {
                if(this.peers[i].peerId === peerId) {
                    this.peers[i].destroy();
                    this.peers.splice(i, 1);
                    break;
                }
            }

            this.change();
        } catch (error) {
            console.error(error.message);
        }
    }

    __resetStreamSource = async function() {
        if(
            !['webcam','screen'].includes(this.__streamSourceType) 
            && !( this.__streamStored instanceof MediaStream)
        ) {
            this.__streamSourceType = 'webcam';
        }

        if(this.__streamSourceType == 'webcam') {
            await this.__startCaptureWebcam();
        } else if(this.__streamSourceType == 'screen') {
            await this.__startCaptureScreen();
        }

        try {
            if(this.__streamStored) {
                if(this.__streamMuted) {
                    var tracks = [...this.__streamStored.getTracks()]
        
                    this.__streamSent = new MediaStream(tracks);
                } else {
                    this.__streamSent = this.__streamStored;
                }

                this.__loadPreview();
            }
        } catch (error) {
            console.error('__resetStreamSource', error);
        }
    }

    __loadPreview = function() {
        try {
            if(this.__previewContainer && this.__previewContainer.nodeName == 'VIDEO') {
                this.__previewContainer.srcObject = this.__streamSent;
            }
        } catch (error) {
            console.error('__loadPreview', error);
        }
    }

    __startCaptureWebcam = async function () {
        var mediaStream = null;
        var constraints = {
            video: true, 
            audio: true,
            // video: {
            //     displaySurface: "window",
            // },
            // audio: {
            //     echoCancellation: true,
            //     noiseSuppression: true,
            //     sampleRate: 44100,
            //     suppressLocalAudioPlayback: true,
            // },
            // surfaceSwitching: "include",
            // selfBrowserSurface: "exclude",
            // systemAudio: "exclude",
        }

        try {
            if(navigator.mediaDevices && navigator.mediaDevices.getUserMedia){ 
                mediaStream = await navigator.mediaDevices.getUserMedia(constraints);
            } else {
                var userMedia = this.__getUserMedia();
    
                if(userMedia) {
                    mediaStream = await(new Promise((res, rej)=>{
                        userMedia(
                            constraints, 
                            (stream) => {
                                res(stream);
                            },
                            (err) => {
                                console.error("__startCaptureWebcam", `The following error occurred: ${err.name}`);
                                res(null);
                            });
                    }));
                } else {
                    console.error("__startCaptureWebcam", "getUserMedia not supported");
                }
            }
        } catch (error) {
            console.error("__startCaptureWebcam", error);
        }

        this.__streamStored = mediaStream;
    }

    __stopCaptureWebcam(evt) {
        try {
            let tracks = this.__streamSent.getTracks();
            tracks.forEach(track => track.stop());

        } catch (error) {
            console.error('__stopCaptureScreen', error);
        }

        this.__streamSent = null;
    }

    __startCaptureScreen = async function () {
        var mediaStream = null;
        try {
            var stream = await navigator.mediaDevices.getDisplayMedia({
                video: {
                    cursor: "always",
                    // width: 1600
                    // height: 1000,
                },
                audio: false
            });

            if(this.__streamMuted) {
                mediaStream = stream;
            } else {
                var voiceStream = await navigator.mediaDevices.getUserMedia({ audio: true, video: false });
                var tracks = [...stream.getTracks(), ...voiceStream.getAudioTracks()]
    
                mediaStream = new MediaStream(tracks);
            }
        } catch (error) {
            console.error("__startCaptureScreen", error);
        }

        this.__streamStored = mediaStream;
    }

    __stopCaptureScreen(evt) {
        try {
            let tracks = this.__streamSent.getTracks();
            tracks.forEach(track => track.stop());
        } catch (error) {
            console.error('__stopCaptureScreen', error);
        }

        this.__streamSent = null;
    }

    __getUserMedia() {
        var medial = navigator.getUserMedia || navigator.webkitGetUserMedia || navigator.mozGetUserMedia || navigator.msGetUserMedia;
    
        return medial;
    }

    __playVideoFile = async function(url) {
        const $this = this;
        try {
            if(!(this.__video && this.__video.nodeName == 'VIDEO')) {
                this.__video = document.createElement("video");
            }
            this.__video.crossOrigin = 'Anonymous';
            this.__video.muted = false;
            this.__video.autoplay = true;
            this.__video.loop = false;
            this.__video.src = url;
    
            this.__video.onplaying = function(e){
                $this.__streamStored = $this.__video.captureStream();
                $this.sendStream($this.__streamStored);
            }
            
            this.__video.play();
            
        } catch (error) {
            console.error('__playVideoFile', error);
        }
    }

    __stopVideoFile = function() {
        try {
            if(this.__video && this.__video.nodeName == 'VIDEO') {
                this.__video.pause();
                this.__video.remove();
                this.__video = null;
            }
        } catch (error) {
            console.error('__stopVideoFile', error);
        }
    }

    /** @returns {WebRTCPeer} */
    __peer(peerId) {
        for (let i = 0; i < this.peers.length; i++) {
            if(this.peers[i].peerId === peerId) {
                return this.peers[i];
            }
        }

        return null;
    }
}

class WebRTCPeer {
    
    constructor(socket, {
        roomId, userId, peerId, 
        localUserId, 
        remotePeerId, 
        remoteProfile,
        localPeerId, 
        localProfile,
        initiator = true
    }) {
        if(!roomId) throw new Error("Room ID is not passed to peer");
        if(!userId) throw new Error("User ID is not passed to peer");
        if(!peerId) throw new Error("Peer ID is not passed to peer");

        this.remoteUsers = [];
        this.initiator = initiator;
        this.browser = getBrowser();
        this.socket = socket;
        this.roomId = roomId;
        this.userId = userId;
        this.peerId = peerId;
        this.remoteProfile = remoteProfile,
        this.remotePeerId = remotePeerId;
        this.localProfile = localProfile,
        this.localUserId = localUserId;
        this.localPeerId = localPeerId;
        /** @type {MediaStream} */
        this.remoteStream = null;

        this.__onPeerDisconnected = null;
        this.__onReadyCallback = null;
        this.__onLeaveCallback = null;
        this.__onJoinCallback = null;
        this.__onConnectedCallback = null;
        this.__onStreamCallback = null;
        this.__onChangeCallback = null;
        this.__onDataCallback = null;
        this.__streamMuted = false;
        /** @type {MediaStream} */
        this.__streamSent = null;

        this.localPeer = new SimplePeer({
            channelName: this.roomId,
            initiator: this.initiator,
            trickle: false
        })

        this.localPeer.on('connect', () => {
            console.log('this.localPeer', 'connect');

            if(typeof this.__onReadyCallback == 'function') {
                this.__onReadyCallback({...this.remoteMetadata});
            }

            if(typeof this.__onConnectedCallback == 'function') {
                this.__onConnectedCallback({...this.remoteMetadata});
            }

            this.__addStream();
        })
 
        this.localPeer.on('signal', (data) => {
            console.log('this.localPeer', 'signal');
            const type = (this.initiator)? 'offer': 'answer';
            const payload = { ...this.metadata }
            payload[type] = data;

            this.socket.emit(type, payload);
            this.__addStream();
        })

        this.localPeer.on('stream', (stream) => {
            this.remoteStream = stream;
            // console.log('stream received', stream);

            try {
                if(typeof this.__onStreamCallback == 'function') {
                    this.__onStreamCallback(stream, {...this.remoteMetadata});
                }
            } catch (error) {
                console.log(error.message);
            }
        })

        this.localPeer.on('data', (data) => {
            // console.log('this.localPeer', 'data');
            let decodedData = new TextDecoder('utf-8').decode(data);

            try {
                var d = JSON.parse(decodedData);
                if(typeof d != 'undefined' && d !== null) {
                    decodedData = d;
                }

                if(typeof this.__onDataCallback == 'function') {
                    this.__onDataCallback(decodedData, {...this.remoteMetadata});
                }
            } catch (error) {
                console.log(error.message);
            }

        })

        //This isn't working in chrome; works perfectly in firefox.
        this.localPeer.on('close', () => {
            console.log('this.localPeer', 'close');

            try {
                if(typeof this.__onPeerDisconnected == 'function') {
                    this.__onPeerDisconnected({...this.metadata})
                }
    
                this.socket.emit('leave', {...this.metadata});
            } catch (error) {
                console.log(error.message);
            }

        })
    }

    triggerJoin() {
        if(typeof this.__onJoinCallback == 'function') {
            this.__onJoinCallback({...this.remoteMetadata});
        }
    }

    async receiveOffer({offer, profile}) {
        this.localPeer.signal(offer);
        this.remoteProfile = profile;

        if(typeof this.__onChangeCallback == 'function'){
            this.__onChangeCallback();
        }
    }

    async receiveAnswer({answer, profile}) {
        this.localPeer.signal(answer);
        this.remoteProfile = profile;

        if(typeof this.__onChangeCallback == 'function'){
            this.__onChangeCallback();
        }
    }

    onReady(callback) {
        this.__onReadyCallback = callback
    }

    onJoin(callback) {
        this.__onJoinCallback = callback;
    }

    onConnected(callback) {
        this.__onConnectedCallback = callback;
    }

    onLeave(callback) {
        this.__onLeaveCallback = callback;
    }

    onStream(callback) {
        this.__onStreamCallback = callback;
    }

    onData(callback) {
        this.__onDataCallback = callback;
    }

    onPeerDisconnected(callback) {
        this.__onPeerDisconnected = callback;
    }

    onPeerChanged(callback) {
        this.__onChangeCallback = callback;
    }

    sendData(data) {
        try {
            if(this.localPeer && this.localPeer.connected) {
                this.localPeer.send(JSON.stringify(data));
            }
        } catch (error) {
            console.error(`${this.logKey}.sendData()`, error.message);
        }
    }

    sendStream(stream) {
        console.log(stream);
        if(stream instanceof MediaStream) {
            this.__streamSent = stream;
        }
        this.__addStream();
    }

    start(stream) {
        this.sendStream(stream);
    }

    stop() {
        this.__removeStream();
    }
    
    destroy() {
        if(this.localPeer) {
            try {
                if(typeof this.__onLeaveCallback == 'function') {
                    this.__onLeaveCallback({...this.remoteMetadata});
                }
    
                this.localPeer.destroy();
            } catch (error) {
                console.error(`${this.logKey}.destroy()`, error.message);
            }
        }
    }

    __onReadyCallback() {

    }
    
    __addStream() {
        this.__removeStream();
        try {
            if(this.__streamSent instanceof MediaStream && this.localPeer && this.localPeer.connected) {
                console.log('stream send', this.__streamSent);

                this.localPeer.addStream(this.__streamSent);
            }
        } catch (error) {
            console.error(`${this.logKey}.__addStream()`, error.message);
        }
    }

    __removeStream() {
        try {
            if(
                this.__streamSent instanceof MediaStream
                && this.__streamSent
            ) {
                // this.localPeer.removeStream(this.__streamSent);
            }
        } catch (error) {
            console.error(`${this.logKey}.__removeStream()`, error.message);
        }
    }

    get metadata() {
        return {
            roomId: this.roomId, 
            peerId: this.localPeerId,
            userId: this.localUserId,
            profile: this.localProfile,
            to: this.peerId,
        }
    }

    get remoteMetadata() {
        return {
            roomId: this.roomId, 
            peerId: this.peerId,
            userId: this.userId,
            profile: this.remoteProfile,
        }
    }

    get logKey() { return this.constructor.name };
}

function copyCandidate(data) {
    if(!data) data = {}

    return {
        address: data.address,
        candidate: data.candidate,
        component: data.component,
        foundation: data.foundation,
        port: data.port,
        priority: data.priority,
        protocol: data.protocol,
        relatedAddress: data.relatedAddress,
        relatedPort: data.relatedPort,
        sdpMLineIndex: data.sdpMLineIndex,
        sdpMid: data.sdpMid,
        tcpType: data.tcpType,
        type: data.type,
        usernameFragment: data.usernameFragment,
    }
}

function getBrowser() {
    var ua=navigator.userAgent,tem,M=ua.match(/(opera|chrome|safari|firefox|msie|trident(?=\/))\/?\s*(\d+)/i) || []; 
    
    if(/trident/i.test(M[1])){
        tem=/\brv[ :]+(\d+)/g.exec(ua) || []; 
        return {name:'IE',version:(tem[1]||'')};
    }   

    if(M[1]==='Chrome'){
        tem=ua.match(/\bOPR|Edge\/(\d+)/)
        if(tem!=null)   {return {name:'Opera', version:tem[1]};}
    }   

    M=M[2]? [M[1], M[2]]: [navigator.appName, navigator.appVersion, '-?'];

    if((tem=ua.match(/version\/(\d+)/i))!=null) {M.splice(1,1,tem[1]);}

    return {
        name: M[0],
        version: M[1]
    };
}