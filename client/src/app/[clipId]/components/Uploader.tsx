"use client"

import React, { useRef } from "react"
import { FaUpload } from "react-icons/fa6"

import ControlButton from "@/components/ControlButton"
import styles from "./Uploader.module.css"

interface Props {
    setFile: (file: File) => void
    disabled: boolean
}

export default function Uploader({ setFile, disabled }: Props) {
    const uploaderRef = useRef<HTMLInputElement>(null)

    const initiateUpload = () => uploaderRef.current?.click()

    const upload = (e: React.ChangeEvent<HTMLInputElement>) => {
        const files = e.target.files
        if (!files || files.length !== 1) { return }
        const file = files[0]

        setFile(file)

        if (uploaderRef.current) {
            uploaderRef.current.value = ""
        }
    }

    return (
        <>
            <ControlButton className={styles.upload} onClick={initiateUpload} disabled={disabled}>
                <FaUpload />
            </ControlButton>
            <input
                ref={uploaderRef}
                type="file"
                onChange={upload}
                style={{ display: "none" }}
            />
        </>
    )
}
