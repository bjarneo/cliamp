#include "listmodel.h"

#include <QJsonObject>

ListModel::ListModel(const QStringList &roles, QObject *parent)
    : QAbstractListModel(parent)
{
    int role = Qt::UserRole + 1;
    for (const QString &name : roles) {
        roles_.insert(role, name.toUtf8());
        roleKeys_.insert(role, name);
        ++role;
    }
}

int ListModel::rowCount(const QModelIndex &parent) const
{
    return parent.isValid() ? 0 : items_.size();
}

QVariant ListModel::data(const QModelIndex &index, int role) const
{
    if (!index.isValid() || index.row() < 0 || index.row() >= items_.size()) {
        return {};
    }

    const auto key = roleKeys_.constFind(role);
    if (key == roleKeys_.cend()) {
        return {};
    }
    return items_.at(index.row()).value(*key);
}

QHash<int, QByteArray> ListModel::roleNames() const
{
    return roles_;
}

QVariantMap ListModel::get(int row) const
{
    if (row < 0 || row >= items_.size()) {
        return {};
    }
    return items_.at(row);
}

void ListModel::setItems(const QJsonArray &items)
{
    QVector<QVariantMap> next;
    next.reserve(items.size());
    for (const QJsonValue &item : items) {
        if (item.isObject()) {
            QVariantMap values = item.toObject().toVariantMap();
            if (values.contains("id")) {
                values.insert("playlistId", values.value("id"));
            }
            next.append(std::move(values));
        }
    }

    beginResetModel();
    items_ = std::move(next);
    endResetModel();
    emit countChanged();
}

void ListModel::clear()
{
    setItems({});
}
