package com.karitham.thriftls

import com.intellij.openapi.components.PersistentStateComponent
import com.intellij.openapi.components.Service
import com.intellij.openapi.components.State
import com.intellij.openapi.components.Storage

@Service(Service.Level.APP)
@State(name = "ThriftLsSettings", storages = [Storage("thrift-ls-jetbrains.xml")])
class ThriftLsSettings : PersistentStateComponent<ThriftLsSettings.Data> {
    class Data {
        var executablePath: String = ""
    }

    @Volatile
    private var data = Data()

    var executablePath: String
        get() = data.executablePath
        set(value) {
            data = Data().apply { executablePath = value }
        }

    override fun getState(): Data = data

    override fun loadState(state: Data) {
        data = state
    }
}
