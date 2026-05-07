"use client"

import { useContext, useEffect, useRef, useState } from "react"
import { FaRegTrashCan, FaRegCopy } from "react-icons/fa6"
import { Watch } from "react-loader-spinner"
import ClipboardJS from "clipboard"

import BodyRefContext from "@/contexts/BodyRefContext"
import MessageQueueContext, { MessageType } from "../contexts/MessageQueueContext"
import { useSocketContents } from "../hooks"
import ControlButton from "@/components/ControlButton"
import Downloader from "./Downloader"
import MessageBox from "./MessageBox"
import Preview from "./Preview"
import Uploader from "./Uploader"
import styles from "./Clipboard.module.css"

interface Props {
    clipId: string
}

export default function Clipboard({ clipId }: Props) {
    const { pushMessage } = useContext(MessageQueueContext)
    const bodyRef = useContext(BodyRefContext)

    const { contents, reset, setText, setFile, socketStatus } = useSocketContents()
    const [renderedContents, setRenderedContents] = useState("")

    const inputRef = useRef<HTMLTextAreaElement>(null)

    useEffect(() => {
        if (contents.type === "text") {
            setRenderedContents(contents.data)
        } else {
            setRenderedContents(`${contents.filename}: ${contents.contentType}`)
        }
    }, [contents])

    const copy = () => {
        if (!inputRef.current) { return }

        ClipboardJS.copy(inputRef.current)
        pushMessage({ type: MessageType.INFO, text: "Copied" })
    }

    const paste = (e: ClipboardEvent) => {
        const items = e.clipboardData?.items
        if (!items || items.length !== 1) { return }
        const item = items[0]

        switch (item.kind) {
            case "string":
                item.getAsString(setText)
                break

            case "file":
                const file = item.getAsFile()
                if (!file) { return }

                setFile(file)
                break

            default:
        }
    }

    useEffect(() => {
        const body = bodyRef.current
        const input = inputRef.current
        if (!body || !input) { return }

        body.onpaste = paste
        body.onkeydown = e => {
            if (e.key === "Escape") { reset() }

            if (e.key === "Enter") {
                input.focus()
                e.preventDefault()
            }
        }

        return () => {
            body.onpaste = null
            body.onkeydown = null
        }
    }, [reset])

    if (socketStatus.type === "Errored") { throw socketStatus.error }

    return (
        <div className={styles.content}>
            <div className={styles.main}>
                <div className={styles.input}>
                    <div className={styles.header}>
                        <strong>{clipId}</strong>
                    </div>

                    <textarea
                        ref={inputRef}
                        value={renderedContents}
                        onChange={e => { setText(e.target.value); setRenderedContents(e.target.value) }}
                        readOnly={contents.type === "file" || socketStatus.type !== "Idle"}
                        className={(contents.type === "file" || socketStatus.type !== "Idle") ? styles["disabled"] : ""}
                        autoFocus
                        rows={10}
                    />

                    <div className={styles["bottom-row"]}>
                        <div className={styles.controls}>
                            <ControlButton className={styles.reset} onClick={reset}>
                                <FaRegTrashCan />
                            </ControlButton>
                            <ControlButton className={styles.copy} onClick={copy}>
                                <FaRegCopy />
                            </ControlButton>
                            <Uploader setFile={setFile} disabled={socketStatus.type !== "Idle"} />
                            <Downloader contents={contents} />
                        </div>

                        <div className={styles.loader}>
                            <div className={styles["chunk-counter"]}>
                                {
                                    (
                                        socketStatus.type === "SendingFile" && socketStatus.nextChunk !== null
                                        || socketStatus.type === "ReceivingFile" && socketStatus.nextChunk !== 0
                                    ) &&
                                    `${socketStatus.type === "SendingFile" ? socketStatus.nextChunk! + 1 : socketStatus.nextChunk}/${socketStatus.header.numChunks}`
                                }
                            </div>
                            {socketStatus.type !== "Idle" && <Watch height={35} width={35} color="#000" />}
                        </div>
                    </div>
                </div>

                <div className={styles["message-box"]}>
                    <MessageBox />
                </div>
            </div>

            <div className={styles.preview} >
                <Preview contents={contents} />
            </div>
        </div>
    )
}
