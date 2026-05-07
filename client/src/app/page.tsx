"use client"

import { useContext, useEffect, useTransition } from "react"
import { Watch } from "react-loader-spinner"

import { clipRedirect } from "./actions"
import ControlButton from "@/components/ControlButton"
import BodyRefContext from "@/contexts/BodyRefContext"
import IdentifierInput from "./components/IdentifierInput"
import styles from "./page.module.css"

export default function Page() {
    const bodyRef = useContext(BodyRefContext)

    const [isPending, startTransition] = useTransition()

    const generateNew = () => {
        startTransition(async () => {
            await clipRedirect(null)
        })
    }

    useEffect(() => {
        const body = bodyRef.current
        if (!body) { return }

        body.onkeydown = e => {
            if (e.key === "Enter") { generateNew() }
        }

        return () => {
            body.onkeydown = null
        }
    }, [])

    return (
        <div className={styles.container}>
            <div className={styles.generate}>
                <ControlButton onClick={generateNew}>Generate New</ControlButton>
            </div>
            <IdentifierInput startTransition={startTransition} />

            {isPending &&
                <div className={styles.loader}>
                    <Watch width={60} height={60} color="#000" />
                </div>
            }
        </div>
    )
}
