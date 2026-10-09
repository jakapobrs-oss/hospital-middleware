// Package middleware contains the Gin middleware that authenticates staff requests.
package middleware

import (
	"github.com/gin-gonic/gin"

	"github.com/jakapobrs-oss/hospital-middleware/internal/domain"
)

const staffIdentityKey = "middleware.staffIdentity"

// SetStaffIdentity stores the authenticated staff member on the request context.
func SetStaffIdentity(c *gin.Context, identity domain.StaffIdentity) {
	c.Set(staffIdentityKey, identity)
}

// StaffIdentityFrom returns the staff member stored by RequireAuth.
// ok is false when the route was not protected by RequireAuth.
func StaffIdentityFrom(c *gin.Context) (identity domain.StaffIdentity, ok bool) {
	value, exists := c.Get(staffIdentityKey)
	if !exists {
		return domain.StaffIdentity{}, false
	}
	identity, ok = value.(domain.StaffIdentity)
	return identity, ok
}
