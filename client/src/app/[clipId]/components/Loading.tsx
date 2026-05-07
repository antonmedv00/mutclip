"use client"

import { Watch } from "react-loader-spinner"
import styles from "./Loading.module.css"

export default function Loading() {
    return (
        <div className={styles.container}>
            <Watch color="#000" />
        </div>
    )
}
