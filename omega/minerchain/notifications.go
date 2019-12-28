// Copyright (c) 2013-2016 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package minerchain

import (
	"github.com/btcsuite/btcd/blockchain"
)

// Subscribe to block chain notifications. Registers a callback to be executed
// when various events take place. See the documentation on Notification and
// NotificationType for details on the types and contents of notifications.
func (b *MinerChain) Subscribe(callback blockchain.NotificationCallback) {
	b.notificationsLock.Lock()
	b.notifications = append(b.notifications, callback)
	b.notificationsLock.Unlock()
}

// sendNotification sends a notification with the passed type and data if the
// caller requested notifications by providing a callback function in the call
// to New.
func (b *MinerChain) sendNotification(typ blockchain.NotificationType, data interface{}) {
	// Generate and send the notification.
	n := blockchain.Notification{Type: typ, Data: data}
	b.notificationsLock.RLock()
	for _, callback := range b.notifications {
		callback(&n)
	}
	b.notificationsLock.RUnlock()
}
