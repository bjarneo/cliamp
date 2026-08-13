#include "ipcclient.h"

#include <QJsonDocument>
#include <QLocalSocket>
#include <QTimer>

namespace {

class IpcRequest final : public QObject
{
public:
    IpcRequest(QString socketPath, QJsonObject request, int timeoutMs, IpcClient::ResponseHandler handler,
               QObject *parent)
        : QObject(parent)
        , request_(std::move(request))
        , handler_(std::move(handler))
    {
        socket_.setParent(this);
        timeout_.setParent(this);
        timeout_.setSingleShot(true);
        timeout_.setInterval(timeoutMs);

        connect(&socket_, &QLocalSocket::connected, this, [this] {
            QByteArray payload = QJsonDocument(request_).toJson(QJsonDocument::Compact);
            payload.append('\n');
            socket_.write(payload);
        });
        connect(&socket_, &QLocalSocket::readyRead, this, [this] { readResponse(); });
        connect(&socket_, &QLocalSocket::errorOccurred, this, [this](QLocalSocket::LocalSocketError) {
            finishError(socket_.errorString());
        });
        connect(&timeout_, &QTimer::timeout, this, [this] { finishError(tr("request timed out")); });

        socket_.connectToServer(socketPath);
        timeout_.start();
    }

private:
    void readResponse()
    {
        buffer_.append(socket_.readAll());
        const qsizetype newline = buffer_.indexOf('\n');
        if (newline < 0) {
            return;
        }

        const QByteArray line = buffer_.left(newline);
        const QJsonDocument response = QJsonDocument::fromJson(line);
        if (!response.isObject()) {
            finishError(tr("invalid response from cliamp"));
            return;
        }
        finish(response.object());
    }

    void finishError(const QString &message)
    {
        finish(QJsonObject{{QStringLiteral("ok"), false}, {QStringLiteral("error"), message}});
    }

    void finish(const QJsonObject &response)
    {
        if (finished_) {
            return;
        }
        finished_ = true;
        timeout_.stop();
        socket_.disconnectFromServer();
        handler_(response);
        deleteLater();
    }

    QLocalSocket socket_;
    QTimer timeout_;
    QByteArray buffer_;
    QJsonObject request_;
    IpcClient::ResponseHandler handler_;
    bool finished_ = false;
};

} // namespace

IpcClient::IpcClient(QString socketPath, QObject *parent)
    : QObject(parent)
    , socketPath_(std::move(socketPath))
{
}

void IpcClient::request(const QJsonObject &request, int timeoutMs, ResponseHandler handler)
{
    new IpcRequest(socketPath_, request, timeoutMs, std::move(handler), this);
}

const QString &IpcClient::socketPath() const
{
    return socketPath_;
}
