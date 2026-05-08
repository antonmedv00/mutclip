"use client"

import { useState, useEffect, useContext, useRef } from "react"

import SocketContext from "./contexts/SocketContext"
import MessageQueueContext, { MessageType } from "./contexts/MessageQueueContext"
import type { Contents } from "./types/clipboard"
import { type FileHeader, Message, type Chunk } from "@/pb/clip"

interface Disconnected {
    type: "Disconnected"
}

interface Errored {
    type: "Errored"
    error: Error
}

interface AwaitingInitialReceive {
    type: "AwaitingInitialReceive"
}

interface Idle {
    type: "Idle"
}

interface SendingText {
    type: "SendingText"
}

interface SendingFile {
    type: "SendingFile"
    header: FileHeader
    nextChunk: number | null
    chunks: Chunk[]
}

interface ReceivingFile {
    type: "ReceivingFile"
    header: FileHeader
    nextChunk: number
    chunks: Chunk[]
}

type SocketState = Disconnected | Errored | AwaitingInitialReceive | Idle | SendingText | SendingFile | ReceivingFile

interface StatusDisconnected {
    type: "Disconnected"
}

interface StatusErrored {
    type: "Errored"
    error: Error
}

interface StatusAwaitingInitialReceive {
    type: "AwaitingInitialReceive"
}

interface StatusIdle {
    type: "Idle"
}

interface StatusSendingText {
    type: "SendingText"
}

interface StatusSendingFile {
    type: "SendingFile"
    header: FileHeader
    nextChunk: number | null
}

interface StatusReceivingFile {
    type: "ReceivingFile"
    header: FileHeader
    nextChunk: number
}

type SocketStatus = StatusDisconnected | StatusErrored | StatusAwaitingInitialReceive | StatusIdle | StatusSendingText | StatusSendingFile | StatusReceivingFile

const cut = async (data: Blob) => {
    const bytes = new Uint8Array(await data.arrayBuffer())

    const chunkSize = 500 * 1024
    const numChunks = Math.ceil(data.size / chunkSize)

    const chunks = []
    for (let i = 0; i < numChunks; i++) {
        chunks.push({
            index: i,
            data: bytes.slice(i * chunkSize, i * chunkSize + chunkSize)
        })
    }

    return chunks
}

// TODO: need a message of denied while receiving file

export function useSocketContents() {
    const { sendMessage, queue, socketOk } = useContext(SocketContext)
    const { pushMessage } = useContext(MessageQueueContext)

    const [contents, setContents] = useState<Contents & { incoming: boolean }>({ type: "text", data: "", incoming: true })

    const socketStateRef = useRef<SocketState>({ type: "Disconnected" })
    const [socketStatus, setSocketStatus] = useState<SocketStatus>({ type: "Disconnected" })

    const getSocketState = () => socketStateRef.current

    const setSocketState = (state: SocketState) => {
        socketStateRef.current = state

        switch (state.type) {
            case "Disconnected":
                setSocketStatus({ type: "Disconnected" })
                break

            case "Errored":
                setSocketStatus({ type: "Errored", error: state.error })
                break

            case "AwaitingInitialReceive":
                setSocketStatus({ type: "AwaitingInitialReceive" })
                break

            case "Idle":
                setSocketStatus({ type: "Idle" })
                break

            case "SendingText":
                setSocketStatus({ type: "SendingText" })
                break

            case "SendingFile":
                setSocketStatus({ type: "SendingFile", header: state.header, nextChunk: state.nextChunk })
                break

            case "ReceivingFile":
                setSocketStatus({ type: "ReceivingFile", header: state.header, nextChunk: state.nextChunk })
                break

            default:
                state satisfies never
        }
    }

    const firstConnection = useRef(true)
    useEffect(() => {
        if (socketOk) {
            if (firstConnection.current) {
                firstConnection.current = false

                setSocketState({ type: "AwaitingInitialReceive" })
            } else {
                setSocketState({ type: "Idle" })
            }
        } else {
            setSocketState({ type: "Disconnected" })
        }
    }, [socketOk])

    useEffect(() => {
        const m = queue.shift()
        if (!m) { return }

        const socketState = getSocketState()

        if (m.text) {
            if (socketState.type !== "Idle" && socketState.type !== "AwaitingInitialReceive") { return }

            setContents({ type: "text", data: m.text.data, incoming: true })

            setSocketState({ type: "Idle" })
        } else if (m.nextChunk) {
            if (socketState.type !== "SendingFile") { return }

            if (socketState.nextChunk === null) {
                setSocketState({ ...socketState, nextChunk: 0 })
            } else {
                setSocketState({ ...socketState, nextChunk: socketState.nextChunk + 1 })
            }
        } else if (m.hdr) {
            if (socketState.type !== "Idle" && socketState.type !== "AwaitingInitialReceive") { return }

            setSocketState({
                type: "ReceivingFile",
                header: m.hdr,
                nextChunk: 0,
                chunks: []
            })

            pushMessage({ type: MessageType.INFO, text: `Receiving ${m.hdr.filename}` })

            sendMessage(Message.create({ nextChunk: {} }))
        } else if (m.chunk) {
            if (socketState.type !== "ReceivingFile") { return }

            if (socketState.nextChunk != m.chunk.index) {
                setSocketState({ type: "Idle" })
                pushMessage({ type: MessageType.ERROR, text: "Transmission disordered" })
                return
            }

            socketState.chunks.push(m.chunk)
            socketState.nextChunk++

            if (socketState.nextChunk < socketState.header.numChunks) {
                setSocketState({ ...socketState })
                sendMessage(Message.create({ nextChunk: {} }))
                return
            }

            const data = new Blob(socketState.chunks.map(chunk => chunk.data as BlobPart))
            setContents({
                type: "file",
                contentType: socketState.header.contentType,
                filename: socketState.header.filename,
                data,
                incoming: true
            })

            setSocketState({ type: "Idle" })
        } else if (m.ack) {
            if (socketState.type !== "SendingText" && socketState.type !== "SendingFile") { return }
            setSocketState({ type: "Idle" })
        } else if (m.err) {
            const err = m.err.msg

            const code = err.slice(0, 7)
            switch (code) {
                case "E000004":
                    setSocketState({ type: "Errored", error: new Error(err) })
                    break

                default:
                    setSocketState({ type: "Idle" })
                    pushMessage({ type: MessageType.ERROR, text: err })
            }
        } else {
            setSocketState({ type: "Idle" })
            pushMessage({ type: MessageType.ERROR, text: "Unexpected message" })
        }
    }, [queue])

    useEffect(() => {
        const socketState = getSocketState()

        if (socketState.type !== "SendingFile") { return }
        if (socketState.nextChunk === null) { return }

        if (socketState.nextChunk >= socketState.header.numChunks) {
            setSocketState({ type: "Idle" })
            pushMessage({ type: MessageType.ERROR, text: "File send might be corrupted" })
            return
        }

        sendMessage(Message.create({
            chunk: socketState.chunks[socketState.nextChunk]
        }))
    }, [socketStatus])

    useEffect(() => {
        if (getSocketState().type !== "Idle" || contents.incoming) { return }

        const chunksPromise = contents.type === "file" ? cut(contents.data) : undefined as never

        const timeout = setTimeout(async () => {
            if (getSocketState().type !== "Idle") { return }

            switch (contents.type) {
                case "text":
                    setSocketState({ type: "SendingText" })

                    sendMessage(Message.create({
                        text: { data: contents.data }
                    }))
                    break

                case "file":
                    const chunks = await chunksPromise
                    const header = { filename: contents.filename, contentType: contents.contentType, numChunks: chunks.length }

                    setSocketState({
                        type: "SendingFile",
                        header,
                        nextChunk: null,
                        chunks
                    })

                    sendMessage(Message.create({ hdr: header }))
                    break

                default:
                    contents satisfies never
            }
        }, 500)

        return () => clearTimeout(timeout)
    }, [contents])

    const reset = () => {
        if (getSocketState().type !== "Idle") { return }

        sendMessage(Message.create({ text: { data: "" } })) // TODO: need special reset message type that would break from file transmission

        setContents({ type: "text", data: "", incoming: true })

        setSocketState({ type: "Idle" })
    }

    const setText = (text: string) => {
        if (getSocketState().type !== "Idle") { return }

        setContents({ type: "text", data: text, incoming: false })
    }

    const setFile = (file: File) => {
        if (getSocketState().type !== "Idle") { return }

        if (file.size > 150 * 1024 * 1024) {
            pushMessage({ type: MessageType.ERROR, text: "Maximum file size is 150 MB" })
            return
        }

        pushMessage({ type: MessageType.INFO, text: `Uploading ${file.name}` })

        const reader = new FileReader()

        reader.onload = async e => {
            const res = e.target?.result
            if (!res || !(res instanceof ArrayBuffer)) { return }

            const type = !file.type || file.type === "text/plain" ? "application/octet-stream" : file.type
            const data = new Blob([res])

            setContents({
                type: "file",
                contentType: type,
                filename: file.name || "file",
                data,
                incoming: false
            })
        }

        reader.readAsArrayBuffer(file)
    }

    return {
        contents,
        reset,
        setText,
        setFile,
        socketStatus
    }
}
