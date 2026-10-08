package com.karitham.thriftls

import com.intellij.execution.ExecutionException
import java.io.File
import java.nio.file.Files
import java.nio.file.Path

internal fun resolveExecutable(configuredPath: String, path: String = System.getenv("PATH") ?: ""): String {
    if (configuredPath.isNotBlank()) {
        val executable = Path.of(configuredPath)
        if (!executable.isAbsolute || !Files.isRegularFile(executable) || !Files.isExecutable(executable)) {
            throw ExecutionException("thrift-ls is not executable at $configuredPath. Set its absolute path in Thrift Language Server settings.")
        }
        return executable.toString()
    }

    val name = if (System.getProperty("os.name").startsWith("Windows")) "thrift-ls.exe" else "thrift-ls"
    for (directory in path.split(File.pathSeparator)) {
        if (directory.isBlank()) continue
        val executable = Path.of(directory, name)
        if (Files.isRegularFile(executable) && Files.isExecutable(executable)) {
            return executable.toAbsolutePath().toString()
        }
    }

    throw ExecutionException("thrift-ls was not found on PATH. Install it or set its absolute path in Thrift Language Server settings.")
}
