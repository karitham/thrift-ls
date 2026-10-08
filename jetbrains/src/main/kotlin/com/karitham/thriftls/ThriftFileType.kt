package com.karitham.thriftls

import com.intellij.icons.AllIcons
import com.intellij.lang.Language
import com.intellij.openapi.fileTypes.LanguageFileType
import javax.swing.Icon

object ThriftLanguage : Language("ThriftLS") {
    override fun getDisplayName(): String = "Thrift"
}

object ThriftFileType : LanguageFileType(ThriftLanguage) {
    override fun getName(): String = "Thrift LS"

    override fun getDescription(): String = "Apache Thrift IDL"

    override fun getDefaultExtension(): String = "thrift"

    override fun getIcon(): Icon = AllIcons.FileTypes.Text
}
