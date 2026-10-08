package com.karitham.thriftls

import com.intellij.openapi.components.service
import com.intellij.openapi.options.SearchableConfigurable
import com.intellij.openapi.project.ProjectManager
import com.intellij.platform.lsp.api.LspClientManager
import com.intellij.ui.components.JBLabel
import com.intellij.ui.components.JBTextField
import java.awt.BorderLayout
import javax.swing.JComponent
import javax.swing.JPanel

class ThriftLsConfigurable : SearchableConfigurable {
    private var executableField: JBTextField? = null

    override fun getId(): String = "com.karitham.thriftls.settings"

    override fun getDisplayName(): String = "Thrift Language Server"

    override fun createComponent(): JComponent {
        val field = JBTextField().also { executableField = it }
        val input = JPanel(BorderLayout(0, 4)).apply {
            add(JBLabel("Absolute path to thrift-ls (empty: search PATH):"), BorderLayout.NORTH)
            add(field, BorderLayout.CENTER)
        }
        return JPanel(BorderLayout()).apply { add(input, BorderLayout.NORTH) }
    }

    override fun isModified(): Boolean =
        executableField?.text?.trim()?.let { it != service<ThriftLsSettings>().executablePath } ?: false

    override fun apply() {
        val settings = service<ThriftLsSettings>()
        val path = executableField?.text?.trim() ?: return
        if (path == settings.executablePath) return

        settings.executablePath = path
        ProjectManager.getInstance().openProjects.forEach { project ->
            if (!project.isDisposed && !project.isDefault) {
                LspClientManager.getInstance(project)
                    .stopAndRestartClientsIfNeeded(ThriftLsIntegrationProvider::class.java)
            }
        }
    }

    override fun reset() {
        executableField?.text = service<ThriftLsSettings>().executablePath
    }

    override fun disposeUIResources() {
        executableField = null
    }
}
