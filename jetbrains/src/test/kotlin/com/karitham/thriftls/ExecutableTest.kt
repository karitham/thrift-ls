package com.karitham.thriftls

import com.intellij.execution.ExecutionException
import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test
import java.io.File
import java.nio.file.Files

class ExecutableTest {
    @Test
    fun `uses a configured executable instead of PATH`() {
        val executable = Files.createTempFile("thrift-ls", "")
        try {
            executable.toFile().setExecutable(true)
            assertEquals(executable.toString(), resolveExecutable(executable.toString(), ""))
        } finally {
            Files.deleteIfExists(executable)
        }
    }

    @Test
    fun `finds a binary on PATH`() {
        val directory = Files.createTempDirectory("thrift-ls-path")
        val name = if (System.getProperty("os.name").startsWith("Windows")) "thrift-ls.exe" else "thrift-ls"
        val executable = Files.createFile(directory.resolve(name))
        try {
            executable.toFile().setExecutable(true)
            assertEquals(executable.toString(), resolveExecutable("", directory.toString()))
        } finally {
            Files.deleteIfExists(executable)
            Files.deleteIfExists(directory)
        }
    }

    @Test
    fun `rejects an invalid configured path and a missing PATH binary`() {
        val directory = Files.createTempDirectory("thrift-ls-missing")
        try {
            assertThrows(ExecutionException::class.java) { resolveExecutable(directory.toString()) }
            assertThrows(ExecutionException::class.java) { resolveExecutable("", directory.toString() + File.pathSeparator) }
        } finally {
            Files.deleteIfExists(directory)
        }
    }
}
