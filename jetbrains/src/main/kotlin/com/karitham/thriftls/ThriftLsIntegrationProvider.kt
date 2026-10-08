package com.karitham.thriftls

import com.intellij.icons.AllIcons
import com.intellij.execution.configurations.GeneralCommandLine
import com.intellij.openapi.components.service
import com.intellij.openapi.project.Project
import com.intellij.openapi.vfs.VirtualFile
import com.intellij.platform.lsp.api.LspClient
import com.intellij.platform.lsp.api.LspIntegrationProvider
import com.intellij.platform.lsp.api.ProjectWideLspClientDescriptor
import com.intellij.platform.lsp.api.lsWidget.LspClientWidgetItem

class ThriftLsIntegrationProvider : LspIntegrationProvider {
    override fun fileOpened(
        project: Project,
        file: VirtualFile,
        clientStarter: LspIntegrationProvider.LspClientStarter,
    ) {
        if (file.extension == "thrift") {
            clientStarter.ensureClientStarted(ThriftLsClientDescriptor(project))
        }
    }

    override fun createWidgetItem(lspClient: LspClient, currentFile: VirtualFile?): LspClientWidgetItem =
        LspClientWidgetItem(lspClient, currentFile, AllIcons.FileTypes.Text, ThriftLsConfigurable::class.java)
}

private class ThriftLsClientDescriptor(project: Project) :
    ProjectWideLspClientDescriptor(project, "thrift-ls") {
    override fun isSupportedFile(file: VirtualFile): Boolean = file.extension == "thrift"

    override fun createCommandLine(): GeneralCommandLine =
        GeneralCommandLine(resolveExecutable(service<ThriftLsSettings>().executablePath), "lsp").apply {
            project.basePath?.let { withWorkDirectory(it) }
        }
}
